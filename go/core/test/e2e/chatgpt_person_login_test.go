package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	kagentenv "github.com/kagent-dev/kagent/go/core/pkg/env"
	"github.com/kagent-dev/kagent/go/core/test/credentialprovider"
	"github.com/kagent-dev/mockllm"
	"github.com/kagent-dev/mockmcp"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The ChatGPT Codex backend of the test, on a fixed Service the installation
// lists as its credential-URI provider (KAGENT_SESSION_ACTOR_JWT_PROVIDERS),
// resolved at the e2e credential provider's authority.
const (
	chatGPTBackendService  = "e2e-chatgpt"
	chatGPTBackendHost     = chatGPTBackendService + ".kagent.svc.cluster.local"
	chatGPTBackendPath     = "/backend-api/codex"
	chatGPTBackendProvider = "e2e-chatgpt"
)

// serveChatGPTBackend serves the mock Responses API at chatGPTBackendPath
// over HTTPS on chatGPTBackendHost, as the ChatGPT Codex backend serves it,
// and records every request it receives.
func serveChatGPTBackend(t *testing.T) *requestRecorder {
	t.Helper()
	config, err := mockllm.LoadConfigFromFile("mocks/invoke_agent.json", interactionMocks)
	require.NoError(t, err)
	routes := mockLLMRoutes(config)
	received := &requestRecorder{}
	backend := received.record(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		rest, ok := strings.CutPrefix(request.URL.Path, chatGPTBackendPath+"/")
		if !ok {
			http.NotFound(w, request)
			return
		}
		request.URL.Path = "/v1/" + rest
		routes.ServeHTTP(w, request)
	}))
	listener, _ := listenOnHost(t)
	serveTLSThroughEgress(t, listener, backend, chatGPTBackendHost)
	kube := interactionKubeClient(t)
	require.NoError(t, exposeOnService(t.Context(), kube, chatGPTBackendService, 443, listener))
	t.Cleanup(func() {
		_ = kube.Delete(t.Context(), &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: chatGPTBackendService, Namespace: "kagent"}})
	})
	return received
}

// requireChatGPTCredential fails unless some request among received reached
// the backend and every one carried, as its bearer, the credential the e2e
// credential provider answered for the Session's Actor, never the placeholder
// codex sent, and no account id.
func requireChatGPTCredential(t *testing.T, received []mockmcp.RecordedRequest, sessionID string) {
	t.Helper()
	require.NotEmpty(t, received, "no request reached the ChatGPT backend")
	want := "Bearer " + credentialprovider.Token(substrate.ActorName(sessionID))
	for _, request := range received {
		require.Equal(t, []string{want}, request.Headers.Values("Authorization"), "%s %s carried another credential than the Session person's", request.Method, request.Path)
		require.Empty(t, request.Headers.Values("Chatgpt-Account-Id"), "%s %s carried an account id: the backend takes a turn without it", request.Method, request.Path)
	}
}

// requestsSince are the requests among received at or after at.
func requestsSince(received []mockmcp.RecordedRequest, at time.Time) []mockmcp.RecordedRequest {
	var result []mockmcp.RecordedRequest
	for _, request := range received {
		if !request.ReceivedAt.Before(at) {
			result = append(result, request)
		}
	}
	return result
}

// A codex agent whose model is the Session person's own ChatGPT login (a
// chatgptOAuth ModelConfig without apiKeySecret) reaches the ChatGPT backend
// with the credential the installation's credential-URI provider answers for
// the Session's person, which the egress gateway sets in place of the
// placeholder codex sends: every bound Session, without a grant, is admitted
// with that provider, and another person's Session gets that person's
// credential. A Session nobody binds lists no provider, so its turn reaches
// the backend with the placeholder alone.
//
// It stays sequential: it rolls the controller onto trusted-proxy
// authentication, which resolves the person of a turn, and onto the backend's
// provider.
func TestChatGPTPersonLoginCredentialDelivery(t *testing.T) {
	target := interactionTarget(t)
	kube := interactionKubeClient(t)
	deployCredentialProvider(t, kube)
	withControllerEnv(t, target, map[string]string{
		kagentenv.AuthMode.Name(): kagentenv.AuthModeTrustedProxy,
		kagentenv.SessionActorJWTProviders.Name(): fmt.Sprintf(`{%q:{"hostname":%q,"pathPrefix":%q,"scheme":"Bearer"}}`,
			chatGPTBackendProvider, chatGPTBackendHost, chatGPTBackendPath+"/"),
		kagentenv.SessionCredentialAuthority.Name(): credentialProviderAuthority,
	})
	received := serveChatGPTBackend(t)

	responses := v1alpha3.OpenAIAPIFormatResponses
	model := &v1alpha3.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "chatgpt-person-", Namespace: "kagent"},
		Spec: v1alpha3.ModelConfigSpec{
			Provider: v1alpha3.ModelProviderOpenAI, Model: "gpt-5.2-codex",
			OpenAI: &v1alpha3.OpenAIConfig{BaseURL: "https://" + chatGPTBackendHost + chatGPTBackendPath, APIFormat: &responses, CredentialType: v1alpha3.OpenAICredentialTypeChatGPTOAuth},
		},
	}
	require.NoError(t, kube.Create(t.Context(), model))
	t.Cleanup(func() { require.NoError(t, ctrlclient.IgnoreNotFound(kube.Delete(t.Context(), model))) })
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "chatgpt-person-", Namespace: "kagent", Labels: testHarness{name: codexE2EHarness, runtimeLabel: "codex"}.labels()},
		Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}, SystemPrompt: "Reply briefly."},
	}
	createAndWaitInteractionTemplateForHarness(t, kube, template, codexE2EHarness)
	bound := func(request *apiv1alpha1.CreateSessionRequest) { request.BindActor = true }

	turn := func(t *testing.T, person string, configure func(*apiv1alpha1.CreateSessionRequest)) (string, []mockmcp.RecordedRequest) {
		t.Helper()
		caller := []string{"authorization", "Bearer " + unsignedToken(t, map[string]any{"sub": person})}
		session := newInteractionFixtureAs(t, target, template.Name, caller, configure)
		started := time.Now()
		_, _, task := session.send(t, "What is 2+2?")
		require.Equal(t, a2atype.TaskStateCompleted, task.Status.State, "task status: %+v", task.Status)
		return session.sessionID, requestsSince(received.Requests(), started)
	}

	alice, aliceRequests := turn(t, "alice@example.com", bound)
	requireChatGPTCredential(t, aliceRequests, alice)
	bob, bobRequests := turn(t, "bob@example.com", bound)
	requireChatGPTCredential(t, bobRequests, bob)
	require.NotEqual(t, aliceRequests[0].Headers.Get("Authorization"), bobRequests[0].Headers.Get("Authorization"), "two people's Sessions share a credential")

	_, unbound := turn(t, "alice@example.com", nil)
	require.NotEmpty(t, unbound, "no request of the unbound Session reached the ChatGPT backend")
	for _, request := range unbound {
		require.NotContains(t, request.Headers.Get("Authorization"), credentialprovider.TokenPrefix, "%s %s of a Session nobody binds carried a person's credential", request.Method, request.Path)
	}
}
