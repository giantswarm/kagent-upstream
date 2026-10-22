package a2agateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aext"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/api/client"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	sessionsvc "github.com/kagent-dev/kagent/go/core/internal/service/session"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// RuntimeDialer connects public gateway calls to the single root Actor used by
// the current v0 Session implementation. Replacing this component with a
// member-store-backed dialer is sufficient when runtime topology becomes
// explicit; the gateway handler does not depend on Actor naming or Atenet.
type RuntimeDialer struct {
	target        string
	transport     credentials.TransportCredentials
	authenticator auth.AuthProvider
	actorJWT      map[string]egress.ActorJWTProvider
	revisions     RuntimeRevisions
}

// RuntimeRevisions reads the runtime revision a Session is prepared on.
type RuntimeRevisions interface {
	GetRuntimeRevision(ctx context.Context, revision string) (*database.RuntimeRevision, error)
}

// RuntimeDialerOption configures a RuntimeDialer.
type RuntimeDialerOption func(*RuntimeDialer)

// WithActorJWTProviders names the provider instances whose hosts receive the
// session Actor's own JWT from the egress gateway, the same the Session
// workflow binds: every turn names those of its session to the runtime.
func WithActorJWTProviders(providers map[string]egress.ActorJWTProvider) RuntimeDialerOption {
	return func(d *RuntimeDialer) { d.actorJWT = providers }
}

// WithRuntimeRevisions reads each Session's prepared revision on dial, so a
// turn carries the caller's credential only to a runtime that sends it to a
// host outside the Session's actor-JWT hosts. Without it every turn carries it.
func WithRuntimeRevisions(revisions RuntimeRevisions) RuntimeDialerOption {
	return func(d *RuntimeDialer) { d.revisions = revisions }
}

// NewRuntimeDialer configures private A2A gRPC calls through Substrate's
// shared Atenet router; ate-target-actor selects the Actor.
func NewRuntimeDialer(routerURL string, authenticator auth.AuthProvider, options ...RuntimeDialerOption) (*RuntimeDialer, error) {
	router, err := url.Parse(routerURL)
	if err != nil {
		return nil, fmt.Errorf("parse Atenet router URL %q: %w", routerURL, err)
	}
	if router.Host == "" {
		return nil, fmt.Errorf("atenet router URL %q must include a host", routerURL)
	}
	if authenticator == nil {
		return nil, fmt.Errorf("atenet runtime authentication is not configured")
	}
	var transport credentials.TransportCredentials
	switch router.Scheme {
	case "http":
		transport = insecure.NewCredentials()
	case "https":
		transport = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: router.Hostname()})
	default:
		return nil, fmt.Errorf("atenet router URL %q must use http or https", routerURL)
	}
	dialer := &RuntimeDialer{target: router.Host, transport: transport, authenticator: authenticator}
	for _, option := range options {
		option(dialer)
	}
	return dialer, nil
}

func (d *RuntimeDialer) Dial(ctx context.Context, session *apiv1alpha1.Session) (*a2aclient.Client, error) {
	targetActor, err := substrate.ActorTargetFromHost(session.GetA2AAuthority())
	if err != nil {
		return nil, err
	}
	actorJWTHosts := sessionsvc.ActorJWTHosts(d.actorJWT, session.GetProviders())
	callerCredential, err := d.forwardsCallerCredential(ctx, session, actorJWTHosts)
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).DebugContext(ctx, "dial session runtime",
		"session_id", session.GetId(), "revision", session.GetPreparedRevision(), "actor_jwt_hosts", actorJWTHosts, "caller_credential", callerCredential)
	return a2aclient.NewFromEndpoints(ctx, []*a2atype.AgentInterface{{
		URL:             d.target,
		ProtocolBinding: a2atype.TransportProtocolGRPC,
		ProtocolVersion: a2atype.Version,
	}},
		a2agrpc.WithGRPCTransport(
			grpc.WithTransportCredentials(d.transport),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(client.DefaultGRPCMaxMessageSize), grpc.MaxCallSendMsgSize(client.DefaultGRPCMaxMessageSize)),
		),
		a2aclient.WithCallInterceptors(
			a2aext.NewClientPropagator(nil),
			&upstreamAuthInterceptor{authenticator: d.authenticator, session: session, targetActor: targetActor, actorJWTHosts: actorJWTHosts, callerCredential: callerCredential},
		),
	)
}

// forwardsCallerCredential reports whether the Session's runtime is handed the
// caller's credential: only when its revision sends it to a host outside the
// Session's actor-JWT hosts, where a runtime sends a placeholder the egress
// gateway replaces with the Actor's own JWT, or to a use outside any host. A
// revision recorded before it named its caller credential is handed it.
func (d *RuntimeDialer) forwardsCallerCredential(ctx context.Context, session *apiv1alpha1.Session, actorJWTHosts []string) (bool, error) {
	if d.revisions == nil {
		return true, nil
	}
	revision, err := d.revisions.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return false, fmt.Errorf("read the runtime revision of session %s: %w", session.GetId(), err)
	}
	return revision.CallerCredential == nil || revision.CallerCredential.Needed(actorJWTHosts), nil
}

// upstreamAuthInterceptor mirrors the current gateway's per-request auth
// forwarding. ServiceParams make the resulting headers transport-neutral: the
// A2A gRPC transport carries them as metadata to the private runtime.
type upstreamAuthInterceptor struct {
	a2aclient.PassthroughInterceptor
	authenticator auth.AuthProvider
	session       *apiv1alpha1.Session
	targetActor   string
	actorJWTHosts []string
	// callerCredential is false when the runtime sends the caller's
	// credential nowhere it is not replaced: the turn then carries none.
	callerCredential bool
}

func (u *upstreamAuthInterceptor) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+req.BaseURL, nil)
	if err != nil {
		return ctx, nil, err
	}
	if session, ok := auth.AuthSessionFrom(ctx); ok {
		principal := auth.Principal{Agent: auth.Agent{ID: u.session.GetId()}}
		if err := u.authenticator.UpstreamAuth(httpRequest, session, principal); err != nil {
			return ctx, nil, err
		}
		if !u.callerCredential {
			httpRequest.Header.Del("Authorization")
		}
		// The runtime keys its session on x-user-id and the context id. On a
		// turn a share of this Session authorizes, the conversation is the
		// owner's: under the visitor's id the runtime finds no session and
		// starts an empty one in the same context. The visitor stays the
		// person of the turn below and keeps their own Authorization.
		if share, ok := auth.ShareContextFrom(ctx); ok && share.IsForSession(u.session.GetId()) {
			httpRequest.Header.Set("X-User-Id", share.UserID)
		}
		// The person of the turn, for the runtime to name on its model calls
		// (x-kagent-user): only an identity the authenticator resolved from the
		// caller's validated token — a session carrying claims. A session
		// without claims (the unsecure mode's X-User-Id and its default, an
		// agent's propagated caller id, the control plane) resolves nobody and
		// forwards nothing, so a raw credential or a stand-in never becomes an
		// accounting identity.
		if caller := session.Principal(); caller.Claims != nil && caller.User.ID != "" {
			httpRequest.Header.Set(adk.UserHeader, caller.User.ID)
		}
	}
	// The Session the turn runs in, for the runtime to name on its model calls
	// (x-kagent-agent-instance-id, the header's name since the AgentInstance
	// days): the Session the gateway routed this call to, never a value the
	// caller sent.
	httpRequest.Header.Set(adk.AgentInstanceHeader, u.session.GetId())
	// The directory the turn works in: its own directory of the session's
	// workspace volume, or a directory of its durable /data.
	httpRequest.Header.Set(adk.WorkingDirectoryHeader, substrate.WorkingDirectory(u.session))
	// What the runtime sets the directory up from before the session's first
	// turn: the mirrors its clones borrow from, and the repositories the
	// admission named as moved since their last sync.
	if mirrors := substrate.MirrorsDirectory(u.session); mirrors != "" {
		httpRequest.Header.Set(adk.MirrorsDirectoryHeader, mirrors)
	}
	if changed := u.session.GetChanged(); u.session.GetVolumeSource() != nil && len(changed) != 0 {
		httpRequest.Header.Set(adk.ChangedRepositoriesHeader, strings.Join(changed, ","))
	}
	// The hosts the egress gateway hands the session Actor's own JWT, where
	// the runtime sends a placeholder and holds no caller credential for.
	if len(u.actorJWTHosts) != 0 {
		httpRequest.Header.Set(adk.ActorJWTHostsHeader, strings.Join(u.actorJWTHosts, ","))
	}
	for key, values := range httpRequest.Header {
		for _, value := range values {
			req.ServiceParams.Append(key, value)
		}
	}
	req.ServiceParams["ate-target-actor"] = []string{u.targetActor}
	return ctx, nil, nil
}
