package translator_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
)

func egressPolicy() (*v1alpha3.EgressPolicy, *corev1.Secret) {
	return &v1alpha3.EgressPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "tickets"}, Spec: v1alpha3.EgressPolicySpec{
		Destinations: []v1alpha3.EgressDestination{{Origin: "https://tickets.example.com", Authentication: &v1alpha3.EgressAuthentication{
			Header: "Authorization", Prefix: new("Bearer "), SecretKeyRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "ticket-auth"}, Key: "token"},
		}}},
	}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "ticket-auth"}, Data: map[string][]byte{"token": []byte("private-ticket-token")}}
}

func TestEgressPolicyCredentialPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		prefix *string
		want   string
	}{
		{name: "bearer token", header: "Authorization", prefix: new("Bearer "), want: "Bearer "},
		{name: "API key", header: "X-API-Key"},
		{name: "empty prefix", header: "X-API-Key", prefix: new("")},
		{name: "custom prefix", header: "Authorization", prefix: new("Token "), want: "Token "},
		{name: "literal prefix", header: "Authorization", prefix: new("Token"), want: "Token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, secret := egressPolicy()
			auth := policy.Spec.Destinations[0].Authentication
			auth.Header, auth.Prefix = tc.header, tc.prefix
			resolution := translator.CompileEgressPolicies(krt.TestingDummyContext{}, mockCollections(t, policy, secret), "test", []corev1.LocalObjectReference{{Name: policy.Name}})
			require.Empty(t, resolution.Issues)
			access := resolution.Access
			wantHeader := "authorization"
			if tc.header == "X-API-Key" {
				wantHeader = "x-api-key"
			}
			require.Equal(t, []egress.Credential{{Hostname: "tickets.example.com", Header: wantHeader, Prefix: tc.want, URI: "ate-secret://k8s.io/default/test/ticket-auth/token"}}, access.Credentials)
		})
	}
}

// An Agent's EgressPolicy grants live on its definition, outside its revision:
// selecting, editing or rotating a policy changes no runtime input, so a grant
// reaches the Agent's running Sessions without a new revision.
func TestAgentEgressPoliciesStayOutOfTheRevision(t *testing.T) {
	for _, kind := range []string{"kagent", "codex", "claude", "byo"} {
		t.Run(kind, func(t *testing.T) {
			policy, secret := egressPolicy()
			harness := &v1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "runner"}, Spec: v1alpha3.HarnessSpec{
				Workload:  v1alpha3.HarnessWorkload{Image: "runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: "default"}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: "snapshots"}},
			}}
			switch kind {
			case "kagent":
				harness.Spec.Kagent = &v1alpha3.KagentHarness{}
			case "codex":
				harness.Spec.Codex = &v1alpha3.CodexHarness{}
			case "claude":
				harness.Spec.Claude = &v1alpha3.ClaudeHarness{}
			case "byo":
				harness.Spec.BYO = &v1alpha3.BYOHarness{}
			}
			model := modelConfig()
			if kind == "codex" {
				model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = secret.Name, "token"
				model.Spec.OpenAI = &v1alpha3.OpenAIConfig{APIFormat: new(v1alpha3.OpenAIAPIFormatResponses)}
			}
			if kind == "claude" {
				model.Spec.Provider, model.Spec.Model = v1alpha3.ModelProviderAnthropic, "claude-sonnet-4-5"
				model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = secret.Name, "token"
			}
			template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "helper"}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}, SystemPrompt: "help"}}
			agent := inlineAgent(harness, template)
			compile := func() *translator.CompileResult {
				result, err := compiler(t, model, harness, policy, secret).CompileAgent(t.Context(), agent)
				require.NoError(t, err)
				return result
			}
			original, err := compile().Digest()
			require.NoError(t, err)
			agent.Spec.EgressPolicyRefs = []corev1.LocalObjectReference{{Name: policy.Name}}
			result := compile()
			require.NotContains(t, result.EgressDestinations, "https://tickets.example.com:443")
			require.NotContains(t, result.Credentials, egress.Credential{Hostname: "tickets.example.com", Header: "authorization", Prefix: "Bearer ", URI: "ate-secret://k8s.io/default/test/ticket-auth/token"})
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), string(secret.Data["token"]))
			selected, err := result.Digest()
			require.NoError(t, err)
			require.Equal(t, original, selected, "selecting a policy changes no runtime input")
			policy.Spec.Destinations = append(policy.Spec.Destinations, v1alpha3.EgressDestination{Origin: "https://registry.npmjs.org"})
			secret.Data["token"], secret.UID = []byte("rotated-token"), "replacement"
			updated, err := compile().Digest()
			require.NoError(t, err)
			require.Equal(t, original, updated, "editing a policy or rotating its Secret changes no runtime input")
		})
	}
}

// A model that passes the caller's token through reserves its host: no
// EgressPolicy credential may replace a header there (egress.FilterGrants).
func TestCallerCredentialRecordsPassthroughHosts(t *testing.T) {
	harness := &v1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "runner"}, Spec: v1alpha3.HarnessSpec{
		Kagent:    &v1alpha3.KagentHarness{},
		Workload:  v1alpha3.HarnessWorkload{Image: "runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: "default"}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: "snapshots"}},
	}}
	model := modelConfig()
	model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = "", ""
	model.Spec.APIKeyPassthrough = true
	template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "helper"}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}, SystemPrompt: "help"}}
	result, err := compiler(t, model, harness).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)
	caller := result.CallerCredential
	require.True(t, caller.PassthroughRecorded)
	require.Equal(t, []string{"api.openai.com"}, caller.PassthroughHosts)
	require.True(t, caller.ReservesCredential("api.openai.com", "x-api-key"))
	require.False(t, caller.ReservesCredential("tickets.example.com", "authorization"))
}

func TestEgressPolicyTLSPassthrough(t *testing.T) {
	policy, secret := egressPolicy()
	policy.Spec.Destinations = []v1alpha3.EgressDestination{{Origin: "tls://DB.example.com:5432"}, {Origin: "tls://*.example.com:443"}}
	resolution := translator.CompileEgressPolicies(krt.TestingDummyContext{}, mockCollections(t, policy, secret), "test", []corev1.LocalObjectReference{{Name: policy.Name}})
	require.Empty(t, resolution.Issues)
	require.Equal(t, []string{"tls://*.example.com:443", "tls://db.example.com:5432"}, resolution.Access.Destinations)
	require.Empty(t, resolution.Access.Credentials)

	policy.Spec.Destinations = []v1alpha3.EgressDestination{{Origin: "tls://db.example.com"}}
	resolution = translator.CompileEgressPolicies(krt.TestingDummyContext{}, mockCollections(t, policy, secret), "test", []corev1.LocalObjectReference{{Name: policy.Name}})
	require.Len(t, resolution.Issues, 1)
	require.Contains(t, resolution.Issues[0].Message, "requires a port")
}

// A missing or invalid policy contributes no grant and an issue, and keeps the
// grants of the others; two policies that conflict are each valid, and their
// union fails admission (egress.CanonicalAccess).
func TestEgressPolicyResolutionFailsClosed(t *testing.T) {
	for _, name := range []string{"missing policy", "other namespace", "missing secret", "missing key", "optional secret", "HTTP auth", "wildcard auth", "TLS auth", "empty auth", "missing header", "protected header", "invalid prefix", "conflicting auth", "conflicting prefix"} {
		t.Run(name, func(t *testing.T) {
			policy, secret := egressPolicy()
			docs := &v1alpha3.EgressPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "docs"}, Spec: v1alpha3.EgressPolicySpec{
				Destinations: []v1alpha3.EgressDestination{{Origin: "https://docs.example.com"}},
			}}
			objects := []any{policy, secret, docs}
			refs := []corev1.LocalObjectReference{{Name: policy.Name}, {Name: docs.Name}}
			switch name {
			case "missing policy":
				objects = []any{secret, docs}
			case "other namespace":
				policy.Namespace = "other"
				resolution := translator.CompileEgressPolicies(krt.TestingDummyContext{}, mockCollections(t, objects...), "test", refs)
				require.Equal(t, []translator.EgressGrantIssue{{Policy: "test/tickets", Reason: v1alpha3.EgressPoliciesReasonNotFound, Message: "EgressPolicy not found"}}, resolution.Issues)
				require.Equal(t, []string{"https://docs.example.com:443"}, resolution.Access.Destinations)
				return
			case "missing secret":
				objects = []any{policy, docs}
			case "missing key":
				secret.Data = nil
			case "optional secret":
				policy.Spec.Destinations[0].Authentication.SecretKeyRef.Optional = new(true)
			case "HTTP auth":
				policy.Spec.Destinations[0].Origin = "http://tickets.example.com"
			case "wildcard auth":
				policy.Spec.Destinations[0].Origin = "https://*.example.com"
			case "TLS auth":
				policy.Spec.Destinations[0].Origin = "tls://tickets.example.com:443"
			case "empty auth":
				policy.Spec.Destinations[0].Authentication = &v1alpha3.EgressAuthentication{}
			case "missing header":
				policy.Spec.Destinations[0].Authentication.Header = ""
			case "protected header":
				policy.Spec.Destinations[0].Authentication.Header = "Host"
			case "invalid prefix":
				policy.Spec.Destinations[0].Authentication.Prefix = new("Bearer\r\nInjected: ")
			case "conflicting auth", "conflicting prefix":
				other := policy.DeepCopy()
				other.Name = "other"
				if name == "conflicting prefix" {
					other.Spec.Destinations[0].Authentication.Prefix = new("Token ")
				} else {
					other.Spec.Destinations[0].Authentication.SecretKeyRef.Key = "other"
					secret.Data["other"] = []byte("different")
				}
				objects = append(objects, other)
				refs = append(refs, corev1.LocalObjectReference{Name: other.Name})
			}
			resolution := translator.CompileEgressPolicies(krt.TestingDummyContext{}, mockCollections(t, objects...), "test", refs)
			if name == "conflicting auth" || name == "conflicting prefix" {
				require.Empty(t, resolution.Issues)
				_, err := egress.CanonicalAccess(resolution.Access.Destinations, resolution.Access.Credentials)
				require.ErrorContains(t, err, "conflicting credentials")
				return
			}
			require.Len(t, resolution.Issues, 1)
			require.Equal(t, "test/tickets", resolution.Issues[0].Policy)
			require.Equal(t, []string{"https://docs.example.com:443"}, resolution.Access.Destinations, "the other policy keeps its grant")
			require.Empty(t, resolution.Access.Credentials)
		})
	}
}

func TestEgressPoliciesMergeCredentialsAndOrigins(t *testing.T) {
	policy, secret := egressPolicy()
	other := policy.DeepCopy()
	other.Name = "registries"
	other.Spec.Destinations = append(other.Spec.Destinations, v1alpha3.EgressDestination{Origin: "https://*.npmjs.org"}, v1alpha3.EgressDestination{
		Origin: "https://tickets.example.com:8443", Authentication: &v1alpha3.EgressAuthentication{Header: "X-API-Key", SecretKeyRef: policy.Spec.Destinations[0].Authentication.SecretKeyRef},
	})
	collections := mockCollections(t, policy, other, secret)
	refs := []corev1.LocalObjectReference{{Name: policy.Name}, {Name: other.Name}}
	resolution := translator.CompileEgressPolicies(krt.TestingDummyContext{}, collections, "test", refs)
	require.Empty(t, resolution.Issues)
	access := resolution.Access
	require.Len(t, access.Destinations, 3)
	require.Len(t, access.Credentials, 2)
	require.Equal(t, "x-api-key", access.Credentials[1].Header)
	refs[0], refs[1] = refs[1], refs[0]
	reversed := translator.CompileEgressPolicies(krt.TestingDummyContext{}, collections, "test", refs)
	require.Equal(t, access, reversed.Access)
}
