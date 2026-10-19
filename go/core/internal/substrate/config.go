package substrate

import (
	"context"
	"time"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

// SessionEgressStore reads the egress a session was admitted with. A session
// the store does not know, such as a sandbox's instance, has none.
type SessionEgressStore interface {
	GetSessionEgress(ctx context.Context, sessionID string) ([]*apiv1.SessionEgressDestination, error)
}

// Config holds connection settings for Agent Substrate ate-api.
type Config struct {
	EgressPolicies egress.PolicyStore
	// SessionEgress adds a session's own egress to its actor's policy. Nil
	// adds none.
	SessionEgress SessionEgressStore
	// AteAPIEndpoint is a gRPC target (e.g. dns:///api.ate-system.svc:443).
	AteAPIEndpoint string
	// CAFile contains the CA certificates used to verify ate-api.
	CAFile string
	// ClientCertFile contains the PEM client certificate and private key used for mTLS.
	ClientCertFile string
	DialTimeout    time.Duration
	CallTimeout    time.Duration
}
