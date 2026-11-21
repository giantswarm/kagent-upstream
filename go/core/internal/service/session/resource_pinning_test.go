package session

import (
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/stretchr/testify/require"
)

const (
	sizingSessionCreator = "alice"
)

func TestSessionCreateUsesPinnedTemplateAfterSizingRevisionChanges(t *testing.T) {
	store, pinned := lifecycleFixture(t)
	initialTemplate := store.revision.ActorTemplateName
	current := supersedingRevision(t, store)
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
	workflow := NewActorWorkflow(store, actors, make(chan struct{}, 1), time.Second)
	created, err := workflow.Create(t.Context(), pinned)
	require.NoError(t, err)
	require.Equal(t, pinned.PreparedRevision, created.PreparedRevision)
	require.Equal(t, initialTemplate, actors.actors[actorKey("team-a", substrate.ActorName(pinned.Id))].GetActorTemplate().GetName())

	newSession, _, err := store.CreateSession(t.Context(), &apiv1alpha1.Session{
		Id: uuid.NewString(), Creator: sizingSessionCreator, Agent: pinned.Agent,
	}, uuid.NewString())
	require.NoError(t, err)
	newSession, err = workflow.Create(t.Context(), newSession)
	require.NoError(t, err)
	require.Equal(t, current.Revision, newSession.PreparedRevision)
	require.Equal(t, current.ActorTemplateName, actors.actors[actorKey("team-a", substrate.ActorName(newSession.Id))].GetActorTemplate().GetName())
}
