package grpcserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/service/serviceerrors"
	sessionsvc "github.com/kagent-dev/kagent/go/core/internal/service/session"
	"github.com/kagent-dev/kagent/go/core/internal/service/session/jwtadmission"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
	"github.com/kagent-dev/kagent/go/core/test/grant"
)

// TestSessionAdmittedByAGrantOnTheWire drives the session server with the jwt
// admission against the PostgreSQL store: a Session created with a grant shows
// the grant's changed list and egress on create, get and list, and the
// grant is in no response and not on the row; a refused grant reserves nothing.
func TestSessionAdmittedByAGrantOnTheWire(t *testing.T) {
	const issuer, audience = "https://workspaces.example.test", "kagent"
	store, db := volumeTestStore(t)
	signer, err := grant.NewSigner(issuer, audience)
	require.NoError(t, err)
	admission, err := jwtadmission.New(jwtadmission.Config{
		JWKSURL: signer.Serve(t), AllowHTTP: true, Issuer: issuer, Audience: audience,
		CredentialAuthority: "ate-secret://credentials.example.test/workspaces",
	}, nil)
	require.NoError(t, err)
	server := &sessionServer{service: sessionsvc.NewService(store, &auth.NoopAuthorizer{}, volumeTestWorkflow{}, sessionsvc.WithVolumeAdmission(admission))}

	ctx := auth.AuthSessionTo(t.Context(), volumeTestSession{userID: "alice"})
	agent := &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}
	source := &apiv1.SessionVolumeSource{
		Volume: &apiv1.SessionVolume{CsiDriver: "nfs.csi.k8s.io", VolumeHandle: "nfs.example.test#exports#workspace-1##"},
		Mounts: []*apiv1.SessionVolumeMount{
			{SubPath: "sessions/${SESSION_ID}", MountPath: "/workspace"},
			{SubPath: "mirrors", MountPath: "/mirrors", ReadOnly: true},
		},
	}
	token, err := signer.SignGrant("alice", grant.Grant{
		AgentNamespace: "team-a",
		Volume:         grant.Volume{Driver: "nfs.csi.k8s.io", Handle: "nfs.example.test#exports#workspace-1##"},
		Mounts:         []grant.Mount{{SubPath: "sessions/${SESSION_ID}"}, {SubPath: "mirrors", ReadOnly: true}},
		Changed:        []string{"acme/api"},
		Egress: []grant.Egress{{Origin: "https://git.example.test", Authentication: &grant.EgressAuthentication{
			Header: "authorization", Prefix: "Basic ", Credential: "git.example.test/git",
		}}},
	}, time.Minute)
	require.NoError(t, err)

	created, err := server.CreateSession(ctx, &apiv1.CreateSessionRequest{Agent: agent, RequestId: "with-grant", VolumeSource: source, AdmissionToken: token})
	require.NoError(t, err)
	id := created.GetSession().GetId()
	got, err := server.GetSession(ctx, &apiv1.GetSessionRequest{SessionId: id})
	require.NoError(t, err)
	listed, err := server.ListSessions(ctx, &apiv1.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetSessions(), 1)
	for name, session := range map[string]*apiv1.Session{"create": created.GetSession(), "get": got.GetSession(), "list": listed.GetSessions()[0]} {
		require.Equal(t, []string{"acme/api"}, session.GetChanged(), "%s shows the changed list", name)
		require.Len(t, session.GetEgress(), 1, "%s shows the egress", name)
		require.Equal(t, "https://git.example.test:443", session.GetEgress()[0].GetOrigin())
		require.Equal(t, "ate-secret://credentials.example.test/workspaces/git.example.test/git", session.GetEgress()[0].GetAuthentication().GetCredentialUri())
	}
	egress, err := store.GetSessionEgress(t.Context(), id)
	require.NoError(t, err)
	require.True(t, proto.Equal(created.GetSession().GetEgress()[0], egress[0]), "the actor's policy reads the admitted egress")
	for name, response := range map[string]proto.Message{"create": created, "get": got, "list": listed} {
		wire, err := proto.Marshal(response)
		require.NoError(t, err)
		require.NotContains(t, string(wire), token, "the %s response must not carry the grant", name)
	}
	var row []byte
	require.NoError(t, db.QueryRow(t.Context(), `SELECT data FROM session WHERE id = $1`, id).Scan(&row))
	require.NotContains(t, string(row), token, "the row must not carry the grant")

	// Bob cannot start a Session with Alice's grant.
	bob := auth.AuthSessionTo(t.Context(), volumeTestSession{userID: "bob"})
	_, err = server.CreateSession(bob, &apiv1.CreateSessionRequest{Agent: agent, RequestId: "stolen-grant", VolumeSource: source, AdmissionToken: token})
	require.True(t, serviceerrors.IsCode(err, serviceerrors.CodePermissionDenied), "error = %v", err)
	require.ErrorContains(t, err, "admission grant names another person")

	// Alice cannot start a Session on an Agent in another namespace with it.
	other := &apiv1.ResourceReference{Namespace: "team-b", Name: "assistant"}
	_, err = server.CreateSession(ctx, &apiv1.CreateSessionRequest{Agent: other, RequestId: "other-namespace", VolumeSource: source, AdmissionToken: token})
	require.True(t, serviceerrors.IsCode(err, serviceerrors.CodePermissionDenied), "error = %v", err)
	require.ErrorContains(t, err, "admission grant is for another namespace")
	listed, err = server.ListSessions(bob, &apiv1.ListSessionsRequest{})
	require.NoError(t, err)
	require.Empty(t, listed.GetSessions(), "a refused grant reserves no Session")
}
