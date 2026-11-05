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

	for _, scenario := range []struct {
		name      string
		namespace string
		want      int
	}{
		{name: "Given a session in team-a, when its creator lists team-a, assert that it is returned", namespace: "team-a", want: 1},
		{name: "Given a session in team-a, when its creator lists team-b, assert that none is returned", namespace: "team-b"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			listed, err := server.ListSessions(ctx, &apiv1alpha1.ListSessionsRequest{Namespace: scenario.namespace})
			require.NoError(t, err)
			require.Len(t, listed.GetSessions(), scenario.want)
		})
	}
}
