package substrate

import (
	"context"
	"fmt"
	"slices"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
)

// EnsureActorEgressPolicy installs current authorization without restarting the
// actor. Read the backend version before the controller's persisted inputs so
// concurrent applications cannot overwrite a newer policy with an old one.
// Conflicts reload both; lifecycle paths never translate Kubernetes.
// Substrate deletes the policy with its Actor.
func (c *Client) EnsureActorEgressPolicy(ctx context.Context, atespace, name, revision string) error {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	if c.cfg.EgressPolicies == nil {
		return fmt.Errorf("actor egress policy store is required")
	}
	actor := actorRef(atespace, name)
	// A session's own egress is fixed when it is admitted, so it is read once.
	session, err := c.sessionEgress(ctx, name)
	if err != nil {
		return fmt.Errorf("load session egress: %w", err)
	}
	for range 5 {
		existing, err := c.GetActorEgressPolicy(ctx, &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		access, err := c.cfg.EgressPolicies.GetRuntimeEgressPolicy(ctx, revision)
		if err != nil {
			return fmt.Errorf("load intended actor egress: %w", err)
		}
		policy, err := ActorEgressPolicy(atespace, access.Destinations, access.Credentials)
		if err != nil {
			return err
		}
		if policy, err = WithSessionEgress(policy, session); err != nil {
			return err
		}
		if existing == nil {
			_, err = c.CreateActorEgressPolicy(ctx, &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: policy})
		} else {
			if !proto.Equal(&ateapipb.EgressPolicy{Rules: existing.GetRules()}, &ateapipb.EgressPolicy{Rules: policy.GetRules()}) {
				policy.Metadata = proto.CloneOf(existing.GetMetadata())
				_, err = c.UpdateActorEgressPolicy(ctx, &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: policy})
			}
		}
		if status.Code(err) == codes.AlreadyExists || status.Code(err) == codes.Aborted {
			continue
		}
		if err != nil {
			return err
		}
		// A source update may have discovered this instance before its Actor
		// existed. Recheck after applying so creation cannot miss that update.
		latest, err := c.cfg.EgressPolicies.GetRuntimeEgressPolicy(ctx, revision)
		if err != nil {
			return fmt.Errorf("reload intended actor egress: %w", err)
		}
		if slices.Equal(access.Destinations, latest.Destinations) && slices.Equal(access.Credentials, latest.Credentials) {
			return nil
		}
	}
	return status.Error(codes.Aborted, "actor egress policy changed during reconciliation")
}

// sessionEgress is the egress the session an actor runs was admitted with:
// none for an actor that runs no session, or without a session egress store.
func (c *Client) sessionEgress(ctx context.Context, actorName string) ([]*apiv1.SessionEgressDestination, error) {
	id, ok := InstanceID(actorName)
	if !ok || c.cfg.SessionEgress == nil {
		return nil, nil
	}
	return c.cfg.SessionEgress.GetSessionEgress(ctx, id)
}
