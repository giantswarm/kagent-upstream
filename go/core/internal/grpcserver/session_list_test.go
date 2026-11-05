package grpcserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	sessionsvc "github.com/kagent-dev/kagent/go/core/internal/service/session"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
)

func TestListSessionsPassesTheNamespaceFilter(t *testing.T) {
	store, _ := volumeTestStore(t)
	server := &sessionServer{service: sessionsvc.NewService(store, &auth.NoopAuthorizer{}, staticSessionWorkflow{})}
	ctx := auth.AuthSessionTo(t.Context(), volumeTestSession{userID: "alice"})
	_, err := server.CreateSession(ctx, &apiv1alpha1.CreateSessionRequest{
		Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "listed",
	})
	require.NoError(t, err)

	for namespace, want := range map[string]int{"team-a": 1, "team-b": 0} {
		listed, err := server.ListSessions(ctx, &apiv1alpha1.ListSessionsRequest{Namespace: namespace})
		require.NoError(t, err)
		require.Len(t, listed.GetSessions(), want, namespace)
	}
}
