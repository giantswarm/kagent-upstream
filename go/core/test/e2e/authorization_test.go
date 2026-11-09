package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	kagentenv "github.com/kagent-dev/kagent/go/core/pkg/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestKubernetesAuthorization switches the controller to the kubernetes
// authorization mode and proves that Kubernetes RBAC decides what a caller
// sees and runs: a person bound in the namespace lists its templates and starts
// a conversation; one bound nowhere gets an empty roster and is refused a
// conversation, over gRPC and over A2A alike. Under trusted-proxy
// authentication a person bound through a group of their token's groups claim
// sees the templates too, and a token that claims a reserved group is refused.
// A Session another caller created is deleted only by a caller RBAC grants a
// cluster-wide delete of sessions: a ServiceAccount granted list alone, and
// another person, are refused, and a person deletes their own with no grant.
//
// It stays sequential: it rolls the controller six times, and while it runs
// the bound person and the test's ServiceAccounts are the only callers the API
// admits.
func TestKubernetesAuthorization(t *testing.T) {
	target := interactionTarget(t)
	kube := interactionKubeClient(t)
	require.NoError(t, rbacv1.AddToScheme(kube.Scheme()))
	require.NoError(t, authenticationv1.AddToScheme(kube.Scheme()))
	deployment := controllerDeployment(t, kube)
	grantAuthorizationFixtures(t, kube, deployment.Spec.Template.Spec.ServiceAccountName)
	withControllerEnv(t, target, map[string]string{kagentenv.AuthorizationMode.Name(): kagentenv.AuthorizationModeKubernetes})

	conn := newControllerConn(t, target)
	templates := apiv1alpha1.NewAgentTemplateServiceClient(conn)
	sessions := apiv1alpha1.NewSessionServiceClient(conn)
	smoke := &apiv1alpha1.ResourceReference{Namespace: "kagent", Name: "smoke"}

	t.Run("a person bound nowhere", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(t.Context(), "x-user-id", "e2e-unbound"), time.Minute)
		defer cancel()
		listed, err := templates.ListAgentTemplates(ctx, &apiv1alpha1.ListAgentTemplatesRequest{Namespace: smoke.Namespace})
		require.NoError(t, err, "an empty roster is an answer, not a refusal")
		require.Empty(t, listed.GetAgentTemplates())
		_, err = sessions.CreateSession(ctx, &apiv1alpha1.CreateSessionRequest{Agent: smoke, RequestId: uuid.NewString()})
		require.Equal(t, codes.PermissionDenied, status.Code(err), "CreateSession() error = %v", err)
		_, request := newMessageRequest(t, "What is 2+2?")
		request.Tenant = smoke.Namespace + "/" + smoke.Name
		_, err = a2apb.NewA2AServiceClient(conn).SendMessage(ctx, request)
		require.Equal(t, codes.PermissionDenied, status.Code(err), "SendMessage() error = %v", err)
	})

	t.Run("a person bound in the namespace", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(t.Context(), "x-user-id", "e2e"), time.Minute)
		defer cancel()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			listed, err := templates.ListAgentTemplates(ctx, &apiv1alpha1.ListAgentTemplatesRequest{Namespace: smoke.Namespace})
			assert.NoError(c, err)
			assert.True(c, slices.ContainsFunc(listed.GetAgentTemplates(), func(template *apiv1alpha1.AgentTemplate) bool {
				return template.GetRef().GetName() == smoke.Name
			}), "the roster lacks %s: %v", smoke.Name, listed.GetAgentTemplates())
		}, time.Minute, time.Second, "the RoleBinding did not take effect")
		// A conversation on an Agent of the namespace, answered by the mock
		// model: the person's turn is unchanged by the mode.
		fixture := newInteractionFixture(t, goADKHarness, target, startInteractionMock(t))
		_, _, task := fixture.send(t, "What is 2+2?")
		require.Equal(t, a2atype.TaskStateCompleted, task.Status.State)
	})

	t.Run("a person bound through a group", func(t *testing.T) {
		withControllerEnv(t, target, map[string]string{
			kagentenv.AuthMode.Name():                 kagentenv.AuthModeTrustedProxy,
			kagentenv.AuthorizationGroupsClaim.Name(): "groups",
		})
		conn := newControllerConn(t, target)
		templates := apiv1alpha1.NewAgentTemplateServiceClient(conn)
		sessions := apiv1alpha1.NewSessionServiceClient(conn)
		as := func(claims map[string]any) context.Context {
			return metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+unsignedToken(t, claims))
		}
		listed := func(ctx context.Context) ([]*apiv1alpha1.AgentTemplate, error) {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			response, err := templates.ListAgentTemplates(ctx, &apiv1alpha1.ListAgentTemplatesRequest{Namespace: smoke.Namespace})
			return response.GetAgentTemplates(), err
		}

		member := as(map[string]any{"sub": "e2e-member", "groups": []string{authorizationGroup}})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			roster, err := listed(member)
			assert.NoError(c, err)
			assert.True(c, slices.ContainsFunc(roster, func(template *apiv1alpha1.AgentTemplate) bool {
				return template.GetRef().GetName() == smoke.Name
			}), "the group member's roster lacks %s: %v", smoke.Name, roster)
		}, time.Minute, time.Second, "the group's RoleBinding did not take effect")

		roster, err := listed(as(map[string]any{"sub": "e2e-member", "groups": []string{"another-team"}}))
		require.NoError(t, err)
		require.Empty(t, roster, "another group admits nothing")

		forged := as(map[string]any{"sub": "e2e-member", "groups": []string{"system:masters"}})
		roster, err = listed(forged)
		require.NoError(t, err)
		require.Empty(t, roster, "a reserved group admits nothing")
		ctx, cancel := context.WithTimeout(forged, time.Minute)
		defer cancel()
		_, err = sessions.CreateSession(ctx, &apiv1alpha1.CreateSessionRequest{Agent: smoke, RequestId: uuid.NewString()})
		require.Equal(t, codes.PermissionDenied, status.Code(err), "CreateSession() error = %v", err)
	})

	t.Run("the delete of another creator's Session", func(t *testing.T) {
		withControllerEnv(t, target, map[string]string{
			kagentenv.AuthMode.Name():                                 kagentenv.AuthModeTrustedProxy,
			kagentenv.AuthorizationServiceAccountTokenAudience.Name(): machineAudience,
		})
		deleter := grantSessionsClusterWide(t, kube, "e2e-session-deleter", "delete")
		lister := grantSessionsClusterWide(t, kube, "e2e-session-lister", "list")
		conn := newControllerConn(t, target)
		sessions := apiv1alpha1.NewSessionServiceClient(conn)
		person := func(name string) []string {
			return []string{"authorization", "Bearer " + unsignedToken(t, map[string]any{"sub": name})}
		}
		as := func(caller []string) context.Context {
			return metadata.AppendToOutgoingContext(t.Context(), caller...)
		}
		// The person "e2e" may create Sessions in the namespace and is granted
		// no delete of sessions anywhere.
		creator := person("e2e")
		erased := newInteractionFixtureAs(t, target, smoke.Name, creator, nil).sessionID
		listed := newInteractionFixtureAs(t, target, smoke.Name, creator, nil).sessionID
		owned := newInteractionFixtureAs(t, target, smoke.Name, creator, nil).sessionID
		const refusal = "Not authorized to delete another creator's Session"

		exists := func(id string) codes.Code {
			ctx, cancel := context.WithTimeout(as(creator), time.Minute)
			defer cancel()
			_, err := sessions.GetSession(ctx, &apiv1alpha1.GetSessionRequest{SessionId: id})
			return status.Code(err)
		}
		// remove deletes id as caller once no lifecycle work owns it. A
		// delete answered NotFound reads as done: exists tells a Session that
		// is gone from one the delete missed.
		remove := func(caller []string, id string) error {
			return deleteSessionWhenIdle(as(caller), sessions, id, deleteSessionBackoff)
		}

		t.Run("a ServiceAccount granted a cluster-wide delete deletes it", func(t *testing.T) {
			caller := []string{"authorization", "Bearer " + serviceAccountToken(t, kube, deleter)}
			require.NoError(t, remove(caller, erased))
			require.Equal(t, codes.NotFound, exists(erased), "the Session outlived its delete")
		})

		t.Run("a ServiceAccount granted a cluster-wide list only is refused", func(t *testing.T) {
			caller := []string{"authorization", "Bearer " + serviceAccountToken(t, kube, lister)}
			// The grant is in effect: the ServiceAccount lists the Session.
			ctx, cancel := context.WithTimeout(as(caller), time.Minute)
			defer cancel()
			page, err := sessions.ListSessions(ctx, &apiv1alpha1.ListSessionsRequest{AllCreators: true, Agent: smoke})
			require.NoError(t, err)
			require.True(t, slices.ContainsFunc(page.GetSessions(), func(session *apiv1alpha1.Session) bool {
				return session.GetId() == listed
			}), "the all-creators list lacks %s", listed)
			err = remove(caller, listed)
			require.Equal(t, codes.PermissionDenied, status.Code(err), "DeleteSession() error = %v", err)
			require.Equal(t, refusal, status.Convert(err).Message())
			require.Equal(t, codes.OK, exists(listed), "the refused delete removed the Session")
		})

		t.Run("a person deletes only their own", func(t *testing.T) {
			err := remove(person("e2e-other"), owned)
			require.Equal(t, codes.PermissionDenied, status.Code(err), "DeleteSession() error = %v", err)
			require.Equal(t, refusal, status.Convert(err).Message())
			require.Equal(t, codes.OK, exists(owned), "the refused delete removed the Session")
			require.NoError(t, remove(creator, owned))
			require.Equal(t, codes.NotFound, exists(owned), "the Session outlived its delete")
		})
	})
}

// machineAudience is the audience the controller requires of a ServiceAccount
// token while the test runs.
const machineAudience = "kagent-e2e"

// grantSessionsClusterWide creates the ServiceAccount name in the kagent
// namespace, bound to verb on every Session cluster-wide, and removes both
// after the test.
func grantSessionsClusterWide(t *testing.T, kube ctrlclient.Client, name, verb string) *corev1.ServiceAccount {
	t.Helper()
	account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kagent"}}
	createForTest(t, kube,
		account,
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Rules:      []rbacv1.PolicyRule{{APIGroups: []string{v1alpha3.GroupVersion.Group}, Resources: []string{"sessions"}, Verbs: []string{verb}}},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: name, Namespace: "kagent"}},
		},
	)
	return account
}

// serviceAccountToken requests a token of account for machineAudience.
func serviceAccountToken(t *testing.T, kube ctrlclient.Client, account *corev1.ServiceAccount) string {
	t.Helper()
	expiration := int64(10 * time.Minute / time.Second)
	request := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{Audiences: []string{machineAudience}, ExpirationSeconds: &expiration}}
	require.NoError(t, kube.SubResource("token").Create(t.Context(), account, request), "request a token of %s", account.Name)
	return request.Status.Token
}

// authorizationGroup is the group the e2e RoleBinding admits beside the person.
const authorizationGroup = "e2e-authorization-team"

// unsignedToken is a JWT with claims and no valid signature, which the
// trusted-proxy authenticator reads as the proxy in front of it would pass it.
func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// grantAuthorizationFixtures binds what the test needs and removes it after:
// the controller's ServiceAccount may create SubjectAccessReviews and
// TokenReviews, which the chart grants only to an install in kubernetes mode,
// and the person "e2e" and the group authorizationGroup may see and run the
// agents of the kagent namespace.
func grantAuthorizationFixtures(t *testing.T, kube ctrlclient.Client, serviceAccount string) {
	t.Helper()
	objects := []ctrlclient.Object{
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-authorization-reviewer"},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{"authorization.k8s.io"}, Resources: []string{"subjectaccessreviews"}, Verbs: []string{"create"}},
				{APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"tokenreviews"}, Verbs: []string{"create"}},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-authorization-reviewer"},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "e2e-authorization-reviewer"},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: serviceAccount, Namespace: "kagent"}},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-authorization-caller", Namespace: "kagent"},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{v1alpha3.GroupVersion.Group}, Resources: []string{"agenttemplates", "agents"}, Verbs: []string{"get", "list"}},
				{APIGroups: []string{v1alpha3.GroupVersion.Group}, Resources: []string{"sessions"}, Verbs: []string{"create"}},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-authorization-caller", Namespace: "kagent"},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "e2e-authorization-caller"},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "e2e"}, {Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: authorizationGroup}},
		},
	}
	createForTest(t, kube, objects...)
}

// createForTest creates objects, leaving one that already exists, and
// deletes each when the test ends.
func createForTest(t *testing.T, kube ctrlclient.Client, objects ...ctrlclient.Object) {
	t.Helper()
	for _, object := range objects {
		require.NoError(t, ctrlclient.IgnoreAlreadyExists(kube.Create(t.Context(), object)), "create %T %s", object, object.GetName())
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := kube.Delete(ctx, object); err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("delete %T %s: %v", object, object.GetName(), err)
			}
		})
	}
}
