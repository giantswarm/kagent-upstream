package translator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
	byotranslator "github.com/kagent-dev/kagent/go/core/internal/translator/byo"
	claudetranslator "github.com/kagent-dev/kagent/go/core/internal/translator/claude"
	codextranslator "github.com/kagent-dev/kagent/go/core/internal/translator/codex"
	kagenttranslator "github.com/kagent-dev/kagent/go/core/internal/translator/kagent"
	"github.com/kagent-dev/kagent/go/core/pkg/env"
	"github.com/stretchr/testify/require"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/krt/krttest"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	compilerTestCollectorEndpoint        = "http://collector:4317"
	compilerTestGRPCProtocol             = "grpc"
	compilerTestOTLPExporter             = "otlp"
	compilerTestTracesExporter           = "OTEL_TRACES_EXPORTER"
	compilerTestBindAddress              = "0.0.0.0"
	compilerTestEndpointField            = "endpoint"
	compilerTestHTTPProtocol             = "http/protobuf"
	compilerTestLowerAuthorizationHeader = "authorization"
	compilerTestMetricsExporter          = "OTEL_METRICS_EXPORTER"
	compilerTestModelCredential          = "model-auth"
	compilerTestNamespace                = "test"
	compilerTestOTLPProtocol             = "OTEL_EXPORTER_OTLP_PROTOCOL"
	compilerTestRunnableAgent            = "runnable-agent"
	compilerTestRuntimeLabel             = "runtime"
	compilerTestSnapshotLocation         = "snapshots"
	compilerTestAPIKeyField              = "api-key"
	compilerTestAccountName              = "account"
	compilerTestAgentImage               = "example.com/agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compilerTestAgentName                = "agent"
	compilerTestAssistantName            = "assistant"
	compilerTestAuthorizationHeader      = "Authorization"
	compilerTestBYOName                  = "byo"
	compilerTestBearerPrefix             = "Bearer "
	compilerTestChildName                = "child"
	compilerTestClaudeModel              = "claude-sonnet-4-5"
	compilerTestCodexName                = "codex"
	compilerTestHarnessName              = "kagent"
	compilerTestHelperName               = "helper"
	compilerTestHostFlag                 = "--host"
	compilerTestKagentImage              = "example.com/kagent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compilerTestLogsExporter             = "OTEL_LOGS_EXPORTER"
	compilerTestLogsProtocol             = "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"
	compilerTestLookupTool               = "lookup"
	compilerTestMCPKind                  = "RemoteMCPServer"
	compilerTestMemoryLimit              = "1Gi"
	compilerTestMissingKey               = "missing key"
	compilerTestMissingName              = "missing"
	compilerTestModelName                = "default-model"
	compilerTestOTLPEndpoint             = "OTEL_EXPORTER_OTLP_ENDPOINT"
	compilerTestOpenAIModel              = "gpt-4o"
	compilerTestOtherName                = "other"
	compilerTestPoolName                 = "default"
	compilerTestPrivateSkillURL          = "https://github.com/acme/private-skills"
	compilerTestPrompt                   = "help"
	compilerTestReferenceSchema          = "references"
	compilerTestResourceAttributes       = "OTEL_RESOURCE_ATTRIBUTES"
	compilerTestReviewSkill              = "review"
	compilerTestRootName                 = "root"
	compilerTestRunbookSkill             = "runbooks"
	compilerTestRuntimeImage             = "example.com/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compilerTestSelectedTool             = "selected"
	compilerTestServiceName              = "OTEL_SERVICE_NAME"
	compilerTestSkillCommit              = "cccccccccccccccccccccccccccccccccccccccc"
	compilerTestTLSOrigin                = "tls://db.example.com:5432"
	compilerTestTokenField               = "token"
	compilerTestTracesEndpoint           = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	compilerTestUnsupportedName          = "unsupported"
)

func modelConfig() *v1alpha3.ModelConfig {
	return &v1alpha3.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestModelName, Namespace: compilerTestNamespace},
		Spec:       v1alpha3.ModelConfigSpec{Provider: v1alpha3.ModelProviderOpenAI, Model: compilerTestOpenAIModel},
	}
}

func TestCompileAgentPreservesWorkloadOverrides(t *testing.T) {
	for _, tt := range []struct {
		name    string
		command []string
		args    []string
	}{
		{name: "image defaults"},
		{name: "command", command: []string{"/runtime"}},
		{name: "args", args: []string{"--verbose"}},
		{name: "go args", args: []string{"--log-level", "debug"}},
		{name: "go command and args", command: []string{"/app"}, args: []string{"--log-level", "debug", compilerTestHostFlag, compilerTestBindAddress}},
		{name: "python static", command: []string{"kagent-adk"}, args: []string{"static", compilerTestHostFlag, compilerTestBindAddress, "--port", "8080"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			harness := &v1alpha3.Harness{
				ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
				Spec: v1alpha3.HarnessSpec{
					Kagent: &v1alpha3.KagentHarness{},

					Workload: v1alpha3.HarnessWorkload{
						Image:   compilerTestRuntimeImage,
						Command: slices.Clone(tt.command), Args: slices.Clone(tt.args),
					},
					Substrate: v1alpha3.RuntimeSubstratePolicy{
						WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
					},
				},
			}
			template := &v1alpha3.AgentTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: compilerTestAgentName, Namespace: compilerTestNamespace},
				Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: compilerTestPrompt},
			}
			original := harness.DeepCopy()
			result, err := compiler(t, modelConfig()).CompileAgent(t.Context(), inlineAgent(harness, template))
			require.NoError(t, err)
			require.Equal(t, tt.command, result.Command)
			require.Equal(t, tt.args, result.Args)
			require.Contains(t, result.Environment, corev1.EnvVar{Name: "KAGENT_PORT", Value: "80"})
			for _, variable := range result.Environment {
				require.NotEqual(t, "KAGENT_A2A_GRPC_ADDRESS", variable.Name)
			}

			revisionID, err := result.Digest()
			require.NoError(t, err)
			if len(tt.command) > 0 || len(tt.args) > 0 {
				withoutOverrides := result.Revision
				withoutOverrides.Command = nil
				withoutOverrides.Args = nil
				defaultID, err := withoutOverrides.Digest()
				require.NoError(t, err)
				require.NotEqual(t, defaultID, revisionID, "workload overrides must affect revision identity")
			}
			actorTemplate, err := substrate.ActorTemplateForRevision(&result.Revision, revisionID)
			require.NoError(t, err)
			require.Len(t, actorTemplate.Containers, 1)
			container := actorTemplate.Containers[0]
			require.Equal(t, tt.command, container.Command)
			require.Equal(t, tt.args, container.Args)

			if len(result.Command) > 0 {
				result.Command[0] = "changed"
			}
			if len(result.Args) > 0 {
				result.Args[0] = "changed"
			}
			require.Equal(t, original, harness, "compiled overrides must not alias the source Harness")
			require.Equal(t, tt.command, container.Command, "container command must not alias the compiled revision")
			require.Equal(t, tt.args, container.Args, "container args must not alias the compiled revision")
		})
	}
}

func TestCompileAgentPinsAgentPluginSources(t *testing.T) {
	embeddingModel := modelConfig()
	embeddingModel.Name = "embedding-model"
	embeddingModel.Spec.Model = "text-embedding-3-small"
	embeddingModel.Spec.TLS = &v1alpha3.TLSConfig{DisableVerify: true}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent: &v1alpha3.KagentHarness{Memory: &v1alpha3.KagentHarnessMemory{
				ModelConfigRef: corev1.LocalObjectReference{Name: embeddingModel.Name}, TTLDays: 7,
			}},

			Workload: v1alpha3.HarnessWorkload{
				Image:   compilerTestKagentImage,
				Command: []string{"kagent-adk", "static"}, Args: []string{compilerTestHostFlag, compilerTestBindAddress},
			},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName},
			Skills: []v1alpha3.AgentTemplateSkill{
				{Name: compilerTestReviewSkill, Source: v1alpha3.ArtifactSource{
					OCI: "ghcr.io/acme/review@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				}},
				{Name: "summary", Source: v1alpha3.ArtifactSource{
					OCI: "acme/summary@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				}},
			},
			Plugins: []v1alpha3.PluginBundle{
				{
					Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
						URL: "https://github.com/acme/plugin", Commit: compilerTestSkillCommit,
					}},
					Skills: []string{"deploy"},
				},
				{Source: v1alpha3.ArtifactSource{Bucket: &v1alpha3.BucketArtifact{S3: v1alpha3.S3Object{
					Endpoint: "https://objects.example.com", Bucket: "plugins", Key: "plugin.zip", VersionID: "version-1",
				}}}},
			},
		},
	}
	spec, err := compiler(t, modelConfig(), embeddingModel).CompileAgent(t.Context(), inlineAgent(harness, template))
	if err != nil {
		t.Fatal(err)
	}
	require.Equal(t, harness.Spec.Workload.Command, spec.Command)
	require.Equal(t, harness.Spec.Workload.Args, spec.Args)
	var config adk.AgentConfig
	if err := json.Unmarshal(spec.ConfigJSON, &config); err != nil {
		t.Fatal(err)
	}
	// The driver is part of the assertion, not incidental. The Python runtime opens
	// this URL with an asyncio engine and refuses a bare `sqlite:` one, so dropping
	// the driver leaves an actor that never serves /readyz — which surfaces as a
	// harness stuck in ResumeGoldenActor rather than as anything naming this line.
	if config.SessionDBURL != "sqlite+aiosqlite:////data/sessions.db" {
		t.Fatalf("session DB URL = %q", config.SessionDBURL)
	}
	if config.Memory == nil || config.Memory.TTLDays != 7 || config.Memory.Embedding == nil || config.Memory.Embedding.TLSInsecureSkipVerify == nil || !*config.Memory.Embedding.TLSInsecureSkipVerify {
		t.Fatalf("compiled memory config = %#v", config.Memory)
	}
	plugins := config.AgentPlugins
	if plugins == nil || len(plugins.Skills) != 2 || len(plugins.Plugins) != 2 || plugins.Plugins[0].Source.Git.Commit != compilerTestSkillCommit {
		t.Fatalf("compiled Agent Plugins config = %#v", config)
	}
	for _, host := range []string{"https://ghcr.io:443", "https://registry-1.docker.io:443", "https://github.com:443", "https://objects.example.com:443"} {
		if !slices.Contains(spec.EgressDestinations, host) {
			t.Fatalf("egress destinations %v do not contain %q", spec.EgressDestinations, host)
		}
	}
}

func remoteMCPServer(name, url string) *v1alpha3.RemoteMCPServer {
	return &v1alpha3.RemoteMCPServer{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: compilerTestNamespace}, Spec: v1alpha3.RemoteMCPServerSpec{
		URL: url, Protocol: v1alpha3.RemoteMCPServerProtocolStreamableHttp,
	}}
}

func compiler(t *testing.T, objects ...any) *translator.Compiler {
	t.Helper()
	collections := mockCollections(t, append(objects, defaultWorkerPool())...)
	ctx := krt.TestingDummyContext{}
	return translator.NewCompiler(ctx, collections, map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeKagent: kagenttranslator.NewCompiler(ctx, collections),
		translator.HarnessTypeCodex:  codextranslator.NewCompiler(ctx, collections),
		translator.HarnessTypeClaude: claudetranslator.NewCompiler(ctx, collections),
		translator.HarnessTypeBYO:    byotranslator.NewCompiler(ctx, collections),
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit})
}

func defaultWorkerPool() *atev1alpha1.WorkerPool {
	return &atev1alpha1.WorkerPool{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestPoolName}}
}

func mockCollections(t *testing.T, objects ...any) translator.Collections {
	t.Helper()
	mock := krttest.NewMock(t, objects)
	collections := translator.Collections{
		EgressPolicies:   krttest.GetMockCollection[*v1alpha3.EgressPolicy](mock),
		AgentTemplates:   krttest.GetMockCollection[*v1alpha3.AgentTemplate](mock),
		Harnesses:        krttest.GetMockCollection[*v1alpha3.Harness](mock),
		RemoteMCPServers: krttest.GetMockCollection[*v1alpha3.RemoteMCPServer](mock),
		ConfigMaps:       krttest.GetMockCollection[*corev1.ConfigMap](mock),
		Secrets:          krttest.GetMockCollection[*corev1.Secret](mock),
		WorkerPools:      krttest.GetMockCollection[*atev1alpha1.WorkerPool](mock),
	}
	models := krttest.GetMockCollection[*v1alpha3.ModelConfig](mock)
	resolved := make([]any, 0, len(models.List()))
	for _, model := range models.List() {
		value, err := translator.ResolveModelConfig(krt.TestingDummyContext{}, collections, model)
		require.NoError(t, err)
		resolved = append(resolved, *value)
	}
	resolvedMock := krttest.NewMock(t, resolved)
	collections.ResolvedModelConfigs = krttest.GetMockCollection[translator.ResolvedModelConfig](resolvedMock)
	return collections
}

func TestCompileAgentResolvesWorkerPoolSandboxClass(t *testing.T) {
	for _, harnessType := range []translator.HarnessType{
		translator.HarnessTypeKagent, translator.HarnessTypeCodex, translator.HarnessTypeClaude, translator.HarnessTypeBYO,
	} {
		t.Run(string(harnessType), func(t *testing.T) {
			harness := &v1alpha3.Harness{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: string(harnessType)},
				Spec: v1alpha3.HarnessSpec{

					Workload: v1alpha3.HarnessWorkload{Image: compilerTestAgentImage},
					Substrate: v1alpha3.RuntimeSubstratePolicy{
						WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestSelectedTool}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
					},
				},
			}
			template := &v1alpha3.AgentTemplate{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestAssistantName},
				Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: compilerTestPrompt},
			}
			model := modelConfig()
			model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = compilerTestModelCredential, compilerTestAPIKeyField
			switch harnessType {
			case translator.HarnessTypeKagent:
				harness.Spec.Kagent = &v1alpha3.KagentHarness{}
			case translator.HarnessTypeCodex:
				harness.Spec.Codex = &v1alpha3.CodexHarness{}
				model.Spec.OpenAI = &v1alpha3.OpenAIConfig{APIFormat: new(v1alpha3.OpenAIAPIFormatResponses)}
				responses := v1alpha3.OpenAIAPIFormatResponses
				model.Spec.OpenAI = &v1alpha3.OpenAIConfig{APIFormat: &responses}
			case translator.HarnessTypeClaude:
				harness.Spec.Claude = &v1alpha3.ClaudeHarness{}
				model.Spec.Provider, model.Spec.Model = v1alpha3.ModelProviderAnthropic, compilerTestClaudeModel
			case translator.HarnessTypeBYO:
				harness.Spec.BYO = &v1alpha3.BYOHarness{}
				harness.Spec.Workload.Command = []string{"/agent"}
				template.Spec.ModelConfig = nil
			}
			originalHarness, originalTemplate := harness.DeepCopy(), template.DeepCopy()
			var baseline *translator.CompileResult
			var defaultDigest translator.RevisionID
			for _, tt := range []struct {
				name    string
				class   atev1alpha1.SandboxClass
				missing bool
			}{
				{name: compilerTestPoolName},
				{name: "explicit gvisor", class: atev1alpha1.SandboxClassGvisor},
				{name: "microvm", class: atev1alpha1.SandboxClassMicroVM},
				{name: "back to gvisor", class: atev1alpha1.SandboxClassGvisor},
				{name: compilerTestUnsupportedName, class: compilerTestUnsupportedName},
				{name: compilerTestMissingName, missing: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					pool := &atev1alpha1.WorkerPool{
						ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestSelectedTool},
						Spec:       atev1alpha1.WorkerPoolSpec{SandboxClass: tt.class},
					}
					originalPool := pool.DeepCopy()
					objects := []any{
						model,
						&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestModelCredential}, Data: map[string][]byte{compilerTestAPIKeyField: []byte("secret")}},
						&atev1alpha1.WorkerPool{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestOtherName, Name: compilerTestSelectedTool}, Spec: atev1alpha1.WorkerPoolSpec{SandboxClass: atev1alpha1.SandboxClassMicroVM}},
						&atev1alpha1.WorkerPool{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: "unselected"}, Spec: atev1alpha1.WorkerPoolSpec{SandboxClass: atev1alpha1.SandboxClassMicroVM}},
					}
					if !tt.missing {
						objects = append(objects, pool)
					}
					result, err := compiler(t, objects...).CompileAgent(t.Context(), inlineAgent(harness, template))
					require.Equal(t, originalHarness, harness)
					require.Equal(t, originalTemplate, template)
					require.Equal(t, originalPool, pool)
					if tt.missing {
						var missing *translator.WorkerPoolNotFoundError
						require.ErrorAs(t, err, &missing)
						require.Equal(t, types.NamespacedName{Namespace: compilerTestNamespace, Name: compilerTestSelectedTool}, missing.WorkerPool)
						require.EqualError(t, err, `WorkerPool "test/selected" not found`)
						require.Nil(t, result, "unresolved capacity must not return a partial revision")
						return
					}
					require.NoError(t, err)
					require.Equal(t, tt.class, result.SandboxClass)
					require.Equal(t, compilerTestSelectedTool, result.WorkerPoolName)
					require.Equal(t, compilerTestNamespace, result.Namespace)
					digest, err := result.Digest()
					if tt.class == compilerTestUnsupportedName {
						require.EqualError(t, err, `unsupported sandbox class "unsupported"`)
						require.True(t, digest.IsZero())
						return
					}
					require.NoError(t, err)
					if baseline == nil {
						baseline, defaultDigest = result, digest
					}
					if tt.class == atev1alpha1.SandboxClassMicroVM {
						require.NotEqual(t, defaultDigest, digest)
					} else {
						require.Equal(t, defaultDigest, digest)
					}
					expected := *baseline
					expected.SandboxClass = tt.class
					require.Equal(t, expected, *result, "sandbox selection must not change other compiled inputs or warnings")
				})
			}
		})
	}
}

func TestCompileAgentStructuredOutput(t *testing.T) {
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent: &v1alpha3.KagentHarness{
				Memory:     &v1alpha3.KagentHarnessMemory{ModelConfigRef: corev1.LocalObjectReference{Name: compilerTestModelName}, TTLDays: 7},
				Compaction: &v1alpha3.KagentHarnessCompaction{CompactionInterval: new(5)},
			},
			Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}},
		},
	}
	schema := `{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}`
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "schemas", Namespace: compilerTestNamespace, UID: "schemas-uid"},
		Data:       map[string]string{"answer.json": schema},
	}
	child := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestChildName, Namespace: compilerTestNamespace, Labels: map[string]string{compilerTestRuntimeLabel: compilerTestHarnessName}},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName},
			// A child contract is intentionally not resolved while this template is nested.
			OutputSchema: &apiextensionsv1.JSON{Raw: []byte(`{"type":"object","oneOf":[]}`)},
		},
	}
	root := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestRootName, Namespace: compilerTestNamespace, Labels: map[string]string{compilerTestRuntimeLabel: compilerTestHarnessName}},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:      &corev1.LocalObjectReference{Name: compilerTestModelName},
			OutputSchemaFrom: &v1alpha3.ConfigMapKeyReference{Name: configMap.Name, Key: "answer.json"},
			Tools: []v1alpha3.ToolBinding{{SubAgent: &v1alpha3.SubAgentToolBinding{
				Name: compilerTestChildName, Description: "delegate", TemplateRef: &corev1.LocalObjectReference{Name: child.Name},
			}}},
		},
	}

	revision, err := compiler(t, modelConfig(), configMap, child).CompileAgent(t.Context(), inlineAgent(harness, root))
	require.NoError(t, err)
	var config adk.AgentConfig
	require.NoError(t, json.Unmarshal(revision.ConfigJSON, &config))
	require.JSONEq(t, schema, string(config.Output.JSONSchema))
	require.Len(t, config.Output.SHA256, 64)
	require.Len(t, config.SubAgents, 1)
	require.Nil(t, config.SubAgents[0].Output)
	require.Equal(t, 7, config.Memory.TTLDays)
	require.Equal(t, 5, *config.ContextConfig.Compaction.CompactionInterval)
	require.Equal(t, "sqlite+aiosqlite:////data/sessions.db", config.SessionDBURL)
	require.Nil(t, config.SubAgents[0].Memory)
	require.Nil(t, config.SubAgents[0].ContextConfig)
	require.Empty(t, config.SubAgents[0].SessionDBURL)
	require.Contains(t, string(revision.Provenance), `"kind":"ConfigMap"`)
	require.Equal(t, []string{"application/json"}, revision.AgentCard.DefaultOutputModes)
}

func TestResolveModelConfigFoundryEndpoint(t *testing.T) {
	ref := &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: compilerTestAccountName}, Key: compilerTestEndpointField}
	const endpoint = "https://example.services.ai.azure.com"
	for _, tt := range []struct {
		name      string
		foundry   *v1alpha3.FoundryConfig
		data      map[string]string
		endpoint  string
		failure   string
		reference bool
	}{
		{name: "inline", foundry: &v1alpha3.FoundryConfig{Endpoint: endpoint}, endpoint: endpoint},
		{name: "inline takes precedence", foundry: &v1alpha3.FoundryConfig{Endpoint: endpoint, EndpointFrom: ref}, endpoint: endpoint},
		{name: "ConfigMap", foundry: &v1alpha3.FoundryConfig{EndpointFrom: ref}, data: map[string]string{compilerTestEndpointField: endpoint}, endpoint: endpoint, reference: true},
		{name: "missing ConfigMap", foundry: &v1alpha3.FoundryConfig{EndpointFrom: ref}, failure: "EndpointConfigMapNotFound", reference: true},
		{name: compilerTestMissingKey, foundry: &v1alpha3.FoundryConfig{EndpointFrom: ref}, data: map[string]string{}, failure: "EndpointConfigMapKeyNotFound", reference: true},
		{name: "empty endpoint", foundry: &v1alpha3.FoundryConfig{EndpointFrom: ref}, data: map[string]string{compilerTestEndpointField: ""}, failure: "EndpointConfigMapKeyEmpty", reference: true},
		{name: "missing endpoint", foundry: &v1alpha3.FoundryConfig{}, failure: "InvalidProviderConfig"},
		{name: "missing provider config", failure: "InvalidProviderConfig"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &v1alpha3.ModelConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "foundry", Namespace: compilerTestNamespace},
				Spec:       v1alpha3.ModelConfigSpec{Model: compilerTestOpenAIModel, Provider: v1alpha3.ModelProviderFoundry, Foundry: tt.foundry},
			}
			original := model.DeepCopy()
			objects := []any{model}
			if tt.data != nil {
				objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: compilerTestNamespace}, Data: tt.data})
			}
			resolved := mockCollections(t, objects...).ResolvedModelConfigs.List()[0]
			require.Equal(t, original, model, "resolution must not mutate its input")
			require.Equal(t, original, resolved.Config, "retain the source configuration separately")
			require.Equal(t, tt.endpoint, resolved.FoundryEndpoint)
			if tt.failure == "" {
				require.True(t, resolved.Usable())
			} else {
				require.False(t, resolved.Usable())
				require.Equal(t, tt.failure, resolved.Failure().Reason)
			}
			if tt.reference {
				require.Equal(t, []translator.ModelConfigReference{{
					NamespacedName: types.NamespacedName{Namespace: compilerTestNamespace, Name: ref.Name}, Kind: "ConfigMap", Key: ref.Key,
				}}, resolved.References)
			} else {
				require.Empty(t, resolved.References)
			}
		})
	}
}

type testHarnessCompiler struct{ input *translator.HarnessInput }

func (c *testHarnessCompiler) Compile(_ context.Context, input *translator.HarnessInput) (*translator.CompileResult, error) {
	c.input = input
	return &translator.CompileResult{Revision: translator.Revision{AgentName: input.AgentName}}, nil
}

func TestCompilerAcceptsExternalHarnessCompiler(t *testing.T) {
	collections := mockCollections(t, modelConfig(), defaultWorkerPool())
	adapter := &testHarnessCompiler{}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestCodexName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Codex:     &v1alpha3.CodexHarness{},
			Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}},
		},
	}
	template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Name: compilerTestAssistantName, Namespace: compilerTestNamespace}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}}}

	revision, err := translator.NewCompiler(krt.TestingDummyContext{}, collections, map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeCodex: adapter,
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit}).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)
	require.Equal(t, compilerTestRunnableAgent, revision.AgentName)
	require.Equal(t, compilerTestRunnableAgent, adapter.input.Root.Template.Name)
	require.Equal(t, modelConfig().Spec, adapter.input.Root.ResolvedModelConfig.Config.Spec)
}

func TestCompilerRejectsStructuredOutputForUnsupportedHarness(t *testing.T) {
	collections := mockCollections(t, modelConfig())
	adapter := &testHarnessCompiler{}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestCodexName, Namespace: compilerTestNamespace},
		Spec:       v1alpha3.HarnessSpec{Codex: &v1alpha3.CodexHarness{}},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestAssistantName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:  &corev1.LocalObjectReference{Name: compilerTestModelName},
			OutputSchema: &apiextensionsv1.JSON{Raw: []byte(`{"type":"object"}`)},
		},
	}

	_, err := translator.NewCompiler(krt.TestingDummyContext{}, collections, map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeCodex: adapter,
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit}).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.ErrorContains(t, err, `Harness runtime "codex" does not support structured output`)
	require.Nil(t, adapter.input)
}

func TestCompilerRejectsUnusableModelConfigBeforeHarnessCompiler(t *testing.T) {
	model := modelConfig()
	model.Spec.APIKeySecret = compilerTestMissingName
	model.Spec.APIKeySecretKey = "key"
	collections := mockCollections(t, model)
	adapter := &testHarnessCompiler{}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestCodexName, Namespace: compilerTestNamespace},
		Spec:       v1alpha3.HarnessSpec{Codex: &v1alpha3.CodexHarness{}},
	}
	template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Name: compilerTestAssistantName, Namespace: compilerTestNamespace}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}}}

	_, err := translator.NewCompiler(krt.TestingDummyContext{}, collections, map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeCodex: adapter,
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit}).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.ErrorContains(t, err, `resolve ModelConfig "default-model": secret missing not found`)
	require.Nil(t, adapter.input)
}

func TestCompilerPermitsBYOWithoutModelConfig(t *testing.T) {
	adapter := &testHarnessCompiler{}
	harness := &v1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Name: compilerTestBYOName, Namespace: compilerTestNamespace}, Spec: v1alpha3.HarnessSpec{
		BYO:       &v1alpha3.BYOHarness{},
		Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}},
	}}
	template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Name: compilerTestAssistantName, Namespace: compilerTestNamespace}}

	_, err := translator.NewCompiler(krt.TestingDummyContext{}, mockCollections(t, defaultWorkerPool()), map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeBYO: adapter,
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit}).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)
	require.Nil(t, adapter.input.Root.ResolvedModelConfig)
}

func TestCompileAgentInjectsCredentialsAtGateway(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-auth", Namespace: compilerTestNamespace},
		Data:       map[string][]byte{compilerTestTokenField: []byte("Bearer top-secret")},
	}
	secondSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "second-mcp-auth", Namespace: compilerTestNamespace},
		Data:       map[string][]byte{compilerTestTokenField: []byte("Bearer another-secret")},
	}
	server := remoteMCPServer("remote", "https://mcp.example.com/mcp")
	server.Spec.HeadersFrom = []v1alpha3.ValueRef{{
		Name: compilerTestAuthorizationHeader,
		ValueFrom: &v1alpha3.ValueSource{
			Type: v1alpha3.SecretValueSource, Name: secret.Name, Key: compilerTestTokenField,
		},
	}}
	secondServer := remoteMCPServer("second-remote", "https://second-mcp.example.com/mcp")
	secondServer.Spec.HeadersFrom = []v1alpha3.ValueRef{{
		Name: compilerTestAuthorizationHeader,
		ValueFrom: &v1alpha3.ValueSource{
			Type: v1alpha3.SecretValueSource, Name: secondSecret.Name, Key: compilerTestTokenField,
		},
	}}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent: &v1alpha3.KagentHarness{},

			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef:  corev1.LocalObjectReference{Name: compilerTestPoolName},
				SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:  &corev1.LocalObjectReference{Name: compilerTestModelName},
			SystemPrompt: compilerTestPrompt,
			Tools: []v1alpha3.ToolBinding{{MCP: &v1alpha3.MCPToolBinding{
				Server: corev1.TypedLocalObjectReference{Kind: compilerTestMCPKind, Name: server.Name},
				Tools:  []string{compilerTestLookupTool},
			}}, {MCP: &v1alpha3.MCPToolBinding{
				Server: corev1.TypedLocalObjectReference{Kind: compilerTestMCPKind, Name: secondServer.Name},
				Tools:  []string{"search"},
			}}},
		},
	}
	compiler := compiler(t, modelConfig(), server, secondServer, secret, secondSecret)
	spec, err := compiler.CompileAgent(t.Context(), inlineAgent(harness, template))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(spec.ConfigJSON, secret.Data[compilerTestTokenField]) || bytes.Contains(spec.Provenance, secret.Data[compilerTestTokenField]) {
		t.Fatal("runtime revision contains credential value")
	}
	if count := bytes.Count(spec.Provenance, []byte(`"kind":"Secret"`)); count != 0 {
		t.Fatalf("provenance contains %d Secret entries, want 0: %s", count, spec.Provenance)
	}
	if !bytes.Contains(spec.ConfigJSON, []byte("__KAGENT_ENV[KAGENT_CREDENTIAL_")) {
		t.Fatalf("config does not contain credential placeholder: %s", spec.ConfigJSON)
	}
	foundSecretValues := map[string]bool{}
	for _, variable := range spec.Environment {
		if variable.ValueFrom != nil {
			t.Fatalf("runtime revision environment contains unresolved valueFrom: %+v", variable)
		}
		foundSecretValues[variable.Value] = true
	}
	if foundSecretValues[string(secret.Data[compilerTestTokenField])] || foundSecretValues[string(secondSecret.Data[compilerTestTokenField])] {
		t.Fatal("credential leaked into runtime environment")
	}
	require.True(t, foundSecretValues[translator.CredentialPlaceholder])
	require.Len(t, spec.Credentials, 2)
	firstDigest, err := spec.Digest()
	require.NoError(t, err)
	rotated := secret.DeepCopy()
	rotated.UID = "replacement-secret"
	rotated.Data[compilerTestTokenField] = []byte("rotated-token")
	rotatedCompiler := translator.NewCompiler(krt.TestingDummyContext{}, mockCollections(t, modelConfig(), server, secondServer, rotated, secondSecret, defaultWorkerPool()), map[translator.HarnessType]translator.HarnessCompiler{
		translator.HarnessTypeKagent: kagenttranslator.NewCompiler(krt.TestingDummyContext{}, mockCollections(t, modelConfig(), server, secondServer, rotated, secondSecret)),
	}, translator.RuntimeDefaults{CPU: "1", Memory: compilerTestMemoryLimit})
	next, err := rotatedCompiler.CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)
	nextDigest, err := next.Digest()
	require.NoError(t, err)
	require.Equal(t, firstDigest, nextDigest, "gateway credential rotation must not change runtime revision")
	if !slices.Equal(spec.EgressDestinations, []string{"http://kagent-controller.kagent:8083", "https://api.openai.com:443", "https://mcp.example.com:443", "https://second-mcp.example.com:443"}) {
		t.Fatalf("egress destinations = %v", spec.EgressDestinations)
	}
}

// A Vertex AI ModelConfig with a service account key compiles for the kagent
// harness: the key is bound on the Google access-token authority for the
// gateway to exchange, the runtime is told to skip Google authentication of
// its own, and nothing of the key reaches the revision.
func TestCompileAgentBindsVertexAIAtGateway(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vertex-auth", Namespace: compilerTestNamespace},
		Data:       map[string][]byte{"credentials.json": []byte(`{"type":"service_account","project_id":"project","token_uri":"https://oauth2.googleapis.com/token","private_key":"private-key-must-not-be-serialized"}`)},
	}
	model := &v1alpha3.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestModelName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.ModelConfigSpec{
			Provider: v1alpha3.ModelProviderAnthropicVertexAI, Model: "claude-sonnet-4-5@20250929",
			APIKeySecret: secret.Name, APIKeySecretKey: "credentials.json",
			AnthropicVertexAI: &v1alpha3.AnthropicVertexAIConfig{BaseVertexAIConfig: v1alpha3.BaseVertexAIConfig{ProjectID: "project", Location: "us-east5"}},
		},
	}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:   &v1alpha3.KagentHarness{},
			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef:  corev1.LocalObjectReference{Name: compilerTestPoolName},
				SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}, SystemPrompt: compilerTestPrompt},
	}
	spec, err := compiler(t, model, secret).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)
	require.Equal(t, []egress.Credential{{
		Hostname: "us-east5-aiplatform.googleapis.com", Header: compilerTestLowerAuthorizationHeader, Prefix: compilerTestBearerPrefix,
		URI: "ate-secret://google-access-token.k8s.io/default/test/vertex-auth/credentials.json",
	}}, spec.Credentials)
	environment := map[string]string{}
	for _, variable := range spec.Environment {
		require.Nil(t, variable.ValueFrom, "%s is unresolved", variable.Name)
		environment[variable.Name] = variable.Value
	}
	require.Equal(t, "true", environment["KAGENT_SKIP_VERTEX_AUTH"])
	require.NotContains(t, environment, "GOOGLE_APPLICATION_CREDENTIALS")
	require.Contains(t, spec.EgressDestinations, "https://us-east5-aiplatform.googleapis.com:443")
	require.NotContains(t, spec.EgressDestinations, "https://oauth2.googleapis.com:443")
	if bytes.Contains(spec.ConfigJSON, []byte("private-key")) || bytes.Contains(spec.Provenance, []byte("private-key")) {
		t.Fatal("runtime revision contains credential material")
	}

	// A key that is not a service account's is reported when the agent is compiled.
	user := secret.DeepCopy()
	user.Data["credentials.json"] = []byte(`{"type":"authorized_user","project_id":"project"}`)
	_, err = compiler(t, model, user).CompileAgent(t.Context(), inlineAgent(harness, template))
	var validation *translator.ValidationError
	require.ErrorAs(t, err, &validation)
	require.ErrorContains(t, err, "service_account")
}

func TestCompileAgentForwardsOtelEnvironment(t *testing.T) {
	t.Setenv(compilerTestTracesExporter, compilerTestOTLPExporter)
	t.Setenv(compilerTestMetricsExporter, compilerTestOTLPExporter)
	t.Setenv(compilerTestLogsExporter, compilerTestOTLPExporter)
	t.Setenv(compilerTestOTLPEndpoint, compilerTestCollectorEndpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://logs:4318/v1/logs")
	t.Setenv(compilerTestLogsProtocol, compilerTestHTTPProtocol)
	otherCollector := "http://other-collector:4317"
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Env:    []v1alpha3.RuntimeEnvVar{{Name: compilerTestTracesEndpoint, Value: otherCollector}},
			Kagent: &v1alpha3.KagentHarness{},

			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef:  corev1.LocalObjectReference{Name: compilerTestPoolName},
				SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:  &corev1.LocalObjectReference{Name: compilerTestModelName},
			SystemPrompt: compilerTestPrompt,
		},
	}
	spec, err := compiler(t, modelConfig()).CompileAgent(t.Context(), inlineAgent(harness, template))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, variable := range spec.Environment {
		found[variable.Name] = variable.Value
	}
	for name, value := range map[string]string{
		compilerTestTracesExporter: compilerTestOTLPExporter, compilerTestMetricsExporter: compilerTestOTLPExporter, compilerTestLogsExporter: compilerTestOTLPExporter,
		compilerTestOTLPEndpoint: compilerTestCollectorEndpoint, compilerTestOTLPProtocol: compilerTestGRPCProtocol,
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://logs:4318/v1/logs", compilerTestLogsProtocol: compilerTestHTTPProtocol,
		compilerTestServiceName:        compilerTestRunnableAgent,
		compilerTestResourceAttributes: "gen_ai.main_agent.id=test/runnable-agent,gen_ai.main_agent.name=runnable-agent,service.namespace=test",
	} {
		if found[name] != value {
			t.Errorf("environment[%s] = %q, want %q", name, found[name], value)
		}
	}
	if _, overridden := found[compilerTestTracesEndpoint]; overridden {
		t.Errorf("Harness trace endpoint survived; egress only allows the controller's collector")
	}
	for _, hostname := range []string{compilerTestCollectorEndpoint, "http://logs:4318"} {
		if !slices.Contains(spec.EgressDestinations, hostname) {
			t.Errorf("%s missing from egress destinations: %v", hostname, spec.EgressDestinations)
		}
	}
}

// The controller tells the runtime which AgentTemplate it executes, so every
// model call the runtime makes can carry that identity.
func TestCompileAgentTemplateIdentifiesTheTemplateToTheRuntime(t *testing.T) {
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:   &v1alpha3.KagentHarness{},
			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef:  corev1.LocalObjectReference{Name: compilerTestPoolName},
				SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:  &corev1.LocalObjectReference{Name: compilerTestModelName},
			SystemPrompt: compilerTestPrompt,
		},
	}
	agent := &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestRunnableAgent}, Spec: v1alpha3.AgentSpec{
		TemplateRef: &corev1.LocalObjectReference{Name: template.Name}, HarnessRef: &corev1.LocalObjectReference{Name: harness.Name},
	}}
	spec, err := compiler(t, modelConfig(), template, harness).CompileAgent(t.Context(), agent)
	if err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{}
	for _, variable := range spec.Environment {
		environment[variable.Name] = variable.Value
	}
	want := map[string]string{"KAGENT_NAME": compilerTestRunnableAgent, "KAGENT_AGENT_TEMPLATE": compilerTestHelperName, "KAGENT_NAMESPACE": compilerTestNamespace}
	for name, value := range want {
		if environment[name] != value {
			t.Errorf("environment[%s] = %q, want %q", name, environment[name], value)
		}
	}
}

func TestCompileAgentSharedADKConfig(t *testing.T) {
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:    &v1alpha3.KagentHarness{},
			Workload:  v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation}},
		},
	}
	child := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "researcher", Namespace: compilerTestNamespace, Labels: map[string]string{compilerTestRuntimeLabel: compilerTestHarnessName}},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, Description: "template description", SystemPrompt: "research carefully",
			Tools: []v1alpha3.ToolBinding{{MCP: &v1alpha3.MCPToolBinding{
				Server: corev1.TypedLocalObjectReference{Kind: compilerTestMCPKind, Name: "search"}, Tools: []string{compilerTestLookupTool},
			}}},
			Skills: []v1alpha3.AgentTemplateSkill{{Name: compilerTestReviewSkill, Source: v1alpha3.ArtifactSource{
				OCI: "ghcr.io/acme/review@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			}}},
		},
	}
	root := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "coordinator", Namespace: compilerTestNamespace, Labels: map[string]string{compilerTestRuntimeLabel: compilerTestHarnessName}},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: "coordinate",
			Skills: child.Spec.Skills,
			Plugins: []v1alpha3.PluginBundle{{Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
				URL: "https://github.com/acme/plugin", Commit: compilerTestSkillCommit,
			}}}},
			Tools: []v1alpha3.ToolBinding{{SubAgent: &v1alpha3.SubAgentToolBinding{
				Name: "web_researcher", Description: "research the web", TemplateRef: &corev1.LocalObjectReference{Name: child.Name},
			}}},
		},
	}

	var commonConfig []byte
	for _, kind := range []string{compilerTestHarnessName, compilerTestBYOName} {
		t.Run(kind, func(t *testing.T) {
			runtimeHarness := harness.DeepCopy()
			if kind == compilerTestBYOName {
				runtimeHarness.Spec.Kagent = nil
				runtimeHarness.Spec.BYO = &v1alpha3.BYOHarness{}
				runtimeHarness.Spec.Workload.Command = []string{"/app"}
			}
			revision, err := compiler(t, modelConfig(), child, remoteMCPServer("search", "https://search.example.com/mcp")).CompileAgent(t.Context(), inlineAgent(runtimeHarness, root))
			require.NoError(t, err)
			var config adk.AgentConfig
			require.NoError(t, json.Unmarshal(revision.ConfigJSON, &config))
			require.Len(t, config.SubAgents, 1)
			require.Equal(t, "web_researcher", config.SubAgents[0].Name)
			require.Equal(t, "research the web", config.SubAgents[0].Description)
			require.Equal(t, "research carefully", config.SubAgents[0].Instruction)
			require.Equal(t, []string{compilerTestLookupTool}, config.SubAgents[0].HttpTools[0].Tools)
			require.Equal(t, compilerTestReviewSkill, config.SubAgents[0].AgentPlugins.Skills[0].Name)
			require.Contains(t, revision.EgressDestinations, "https://search.example.com:443")
			require.Contains(t, revision.EgressDestinations, "https://ghcr.io:443")
			require.Contains(t, string(revision.Provenance), `"name":"researcher"`)

			require.Equal(t, "coordinate", config.Instruction)
			require.NotNil(t, config.Model)
			require.True(t, config.GetStream())
			require.Equal(t, compilerTestReviewSkill, config.AgentPlugins.Skills[0].Name)
			require.Equal(t, "https://github.com/acme/plugin", config.AgentPlugins.Plugins[0].Source.Git.URL)
			require.Equal(t, "sqlite+aiosqlite:////data/sessions.db", config.SessionDBURL)
			require.Empty(t, config.SubAgents[0].SessionDBURL)
			require.Nil(t, config.Memory)
			require.Nil(t, config.ContextConfig)
			require.Nil(t, config.Output)
			if commonConfig == nil {
				commonConfig = revision.ConfigJSON
			} else {
				require.JSONEq(t, string(commonConfig), string(revision.ConfigJSON))
			}
		})
	}
}

func TestCompileAgentRejectsInvalidSharedTrees(t *testing.T) {
	harness := &v1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace}, Spec: v1alpha3.HarnessSpec{
		Kagent: &v1alpha3.KagentHarness{},
	}}
	binding := func(name, target string) v1alpha3.ToolBinding {
		return v1alpha3.ToolBinding{SubAgent: &v1alpha3.SubAgentToolBinding{Name: name, Description: name, TemplateRef: &corev1.LocalObjectReference{Name: target}}}
	}
	template := func(name string, tools ...v1alpha3.ToolBinding) *v1alpha3.AgentTemplate {
		return &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: compilerTestNamespace, Labels: map[string]string{compilerTestRuntimeLabel: compilerTestHarnessName}}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, Tools: tools}}
	}

	t.Run("shared DAG", func(t *testing.T) {
		child := template(compilerTestChildName)
		root := template(compilerTestRootName, binding("first", child.Name), binding("second", child.Name))
		_, err := compiler(t, child).CompileAgent(t.Context(), inlineAgent(harness, root))
		require.ErrorContains(t, err, "referenced more than once")
	})
	t.Run("cycle", func(t *testing.T) {
		root := template(compilerTestRootName, binding(compilerTestChildName, compilerTestChildName))
		child := template(compilerTestChildName, binding(compilerTestRootName, root.Name))
		_, err := compiler(t, root, child, harness).CompileAgent(t.Context(), &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Name: compilerTestRunnableAgent, Namespace: compilerTestNamespace}, Spec: v1alpha3.AgentSpec{TemplateRef: &corev1.LocalObjectReference{Name: root.Name}, HarnessRef: &corev1.LocalObjectReference{Name: harness.Name}}})
		require.ErrorContains(t, err, "cycle")
	})
	t.Run("consecutive shared depth", func(t *testing.T) {
		root := template(compilerTestRootName, binding(compilerTestChildName, compilerTestChildName))
		child := template(compilerTestChildName, binding("grandchild", "grandchild"))
		grandchild := template("grandchild")
		_, err := compiler(t, child, grandchild).CompileAgent(t.Context(), inlineAgent(harness, root))
		require.ErrorContains(t, err, "consecutive Shared")
	})
}

func TestCompileAgentRejectsInvalidSubagentReferences(t *testing.T) {
	harness := &v1alpha3.Harness{Spec: v1alpha3.HarnessSpec{Kagent: &v1alpha3.KagentHarness{}}}
	for _, tt := range []struct {
		name      string
		binding   v1alpha3.SubAgentToolBinding
		wantError string
	}{
		{
			name:      "missing reference",
			wantError: "requires templateRef.name",
		},
		{
			name:      "empty reference",
			binding:   v1alpha3.SubAgentToolBinding{TemplateRef: &corev1.LocalObjectReference{}},
			wantError: "requires templateRef.name",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.binding.Name, tt.binding.Description = compilerTestReviewSkill, "Review code"
			template := &v1alpha3.AgentTemplate{Spec: v1alpha3.AgentTemplateSpec{
				Tools: []v1alpha3.ToolBinding{{SubAgent: &tt.binding}},
			}}
			_, err := compiler(t).CompileAgent(t.Context(), inlineAgent(harness, template))
			require.ErrorContains(t, err, tt.wantError)
			var validationError *translator.ValidationError
			require.ErrorAs(t, err, &validationError)
		})
	}
}

func TestCompileAgentInlineAndReferencedConfiguration(t *testing.T) {
	template := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestAssistantName, UID: "template-uid"}, Spec: v1alpha3.AgentTemplateSpec{
		ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: "review code",
		Tools: []v1alpha3.ToolBinding{{SubAgent: &v1alpha3.SubAgentToolBinding{Name: "reviewer", Description: "delegate review", TemplateRef: &corev1.LocalObjectReference{Name: compilerTestChildName}}}},
	}}
	child := &v1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestChildName}, Spec: v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: "review security"}}
	harness := &v1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestRuntimeLabel, UID: "harness-uid"}, Spec: v1alpha3.HarnessSpec{
		Kagent: &v1alpha3.KagentHarness{}, Workload: v1alpha3.HarnessWorkload{Image: compilerTestAgentImage},
		Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation}},
	}}
	for _, tt := range []struct {
		name                          string
		inlineTemplate, inlineHarness bool
	}{
		{compilerTestReferenceSchema, false, false}, {"inline template", true, false}, {"inline harness", false, true}, {"inline both", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			agent := &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestAssistantName, UID: "agent-uid"}}
			if tt.inlineTemplate {
				agent.Spec.Template = template.Spec.DeepCopy()
			} else {
				agent.Spec.TemplateRef = &corev1.LocalObjectReference{Name: template.Name}
			}
			if tt.inlineHarness {
				agent.Spec.Harness = harness.Spec.DeepCopy()
			} else {
				agent.Spec.HarnessRef = &corev1.LocalObjectReference{Name: harness.Name}
			}
			original := agent.DeepCopy()
			c := compiler(t, modelConfig(), template, harness, child)
			result, err := c.CompileAgent(t.Context(), agent)
			require.NoError(t, err)
			require.Equal(t, compilerTestAssistantName, result.AgentName)
			require.Equal(t, "agent-uid", result.AgentUID)
			require.Contains(t, string(result.ConfigJSON), "review security")
			require.Equal(t, original, agent, "compilation must not mutate inline specs")
			first, err := result.Digest()
			require.NoError(t, err)
			agent.UID = "replacement-uid"
			replacement, err := c.CompileAgent(t.Context(), agent)
			require.NoError(t, err)
			second, err := replacement.Digest()
			require.NoError(t, err)
			require.NotEqual(t, first, second, "recreated Agents must not reuse the previous identity")
		})
	}
	t.Run("inline root can reference a template with the same name", func(t *testing.T) {
		agent := inlineAgent(harness, template)
		agent.Name = child.Name
		result, err := compiler(t, modelConfig(), child).CompileAgent(t.Context(), agent)
		require.NoError(t, err)
		require.Contains(t, string(result.ConfigJSON), "review security")
		var provenance struct{ Inputs []struct{ Kind, Name string } }
		require.NoError(t, json.Unmarshal(result.Provenance, &provenance))
		for _, input := range provenance.Inputs {
			require.NotEqual(t, "Harness", input.Kind, "inline Harness has no standalone resource")
			if input.Kind == "AgentTemplate" {
				require.Equal(t, child.Name, input.Name, "only the referenced child is a standalone template")
			}
		}
	})
	t.Run("missing local reference", func(t *testing.T) {
		agent := &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: "elsewhere", Name: compilerTestAssistantName}, Spec: v1alpha3.AgentSpec{TemplateRef: &corev1.LocalObjectReference{Name: template.Name}, HarnessRef: &corev1.LocalObjectReference{Name: harness.Name}}}
		_, err := compiler(t, template, harness).CompileAgent(t.Context(), agent)
		require.ErrorContains(t, err, "not found")
	})
}

// Runtime identity also selects the ADK memory namespace. Reusing configuration
// must not merge Agents' memories or change an Agent's namespace when inlined.
func TestCompileAgentRuntimeIdentity(t *testing.T) {
	t.Setenv(compilerTestTracesExporter, compilerTestOTLPExporter)
	t.Setenv(compilerTestOTLPEndpoint, compilerTestCollectorEndpoint)
	for _, kind := range []translator.HarnessType{
		translator.HarnessTypeKagent, translator.HarnessTypeCodex,
		translator.HarnessTypeClaude, translator.HarnessTypeBYO,
	} {
		t.Run(string(kind), func(t *testing.T) {
			model := modelConfig()
			model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = compilerTestModelCredential, compilerTestAPIKeyField
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestModelCredential}, Data: map[string][]byte{compilerTestAPIKeyField: []byte("test-key")}}
			template := &v1alpha3.AgentTemplate{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: "shared-context"},
				Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: model.Name}, SystemPrompt: compilerTestPrompt},
			}
			harness := &v1alpha3.Harness{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: "shared-runtime"},
				Spec: v1alpha3.HarnessSpec{
					Workload:  v1alpha3.HarnessWorkload{Image: compilerTestRuntimeImage},
					Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation}},
				},
			}
			switch kind {
			case translator.HarnessTypeKagent:
				harness.Spec.Kagent = &v1alpha3.KagentHarness{Memory: &v1alpha3.KagentHarnessMemory{ModelConfigRef: corev1.LocalObjectReference{Name: model.Name}}}
			case translator.HarnessTypeCodex:
				harness.Spec.Codex = &v1alpha3.CodexHarness{}
				model.Spec.OpenAI = &v1alpha3.OpenAIConfig{APIFormat: new(v1alpha3.OpenAIAPIFormatResponses)}
			case translator.HarnessTypeClaude:
				harness.Spec.Claude = &v1alpha3.ClaudeHarness{}
				model.Spec.Provider, model.Spec.Model = v1alpha3.ModelProviderAnthropic, compilerTestClaudeModel
			case translator.HarnessTypeBYO:
				harness.Spec.BYO = &v1alpha3.BYOHarness{}
			}
			c := compiler(t, model, template, harness, secret)
			for _, name := range []string{"finance-reviewer", "support-reviewer"} {
				for _, source := range []struct {
					name              string
					template, harness bool
				}{
					{compilerTestReferenceSchema, false, false}, {"inline-template", true, false},
					{"inline-harness", false, true}, {"inline-both", true, true},
				} {
					t.Run(name+"/"+source.name, func(t *testing.T) {
						agent := &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: name}, Spec: v1alpha3.AgentSpec{
							TemplateRef: &corev1.LocalObjectReference{Name: template.Name}, HarnessRef: &corev1.LocalObjectReference{Name: harness.Name},
						}}
						if source.template {
							agent.Spec.TemplateRef, agent.Spec.Template = nil, template.Spec.DeepCopy()
						}
						if source.harness {
							agent.Spec.HarnessRef, agent.Spec.Harness = nil, harness.Spec.DeepCopy()
						}
						result, err := c.CompileAgent(t.Context(), agent)
						require.NoError(t, err)
						environment := map[string]string{}
						for _, variable := range result.Environment {
							environment[variable.Name] = variable.Value
						}
						if kind != translator.HarnessTypeBYO {
							require.Equal(t, name, environment["KAGENT_NAME"])
							require.Equal(t, agent.Namespace, environment["KAGENT_NAMESPACE"])
						}
						require.Equal(t, name, environment[compilerTestServiceName])
						require.Contains(t, environment[compilerTestResourceAttributes], "gen_ai.main_agent.id=test/"+name)
						require.Equal(t, strings.ReplaceAll(name, "-", "_"), result.AgentCard.Name)
						if kind == translator.HarnessTypeKagent {
							var config adk.AgentConfig
							require.NoError(t, json.Unmarshal(result.ConfigJSON, &config))
							require.NotNil(t, config.Memory)
						}
					})
				}
			}
		})
	}
}

// inlineAgent supplies complete specs through the same public entry point as users.
func inlineAgent(harness *v1alpha3.Harness, template *v1alpha3.AgentTemplate) *v1alpha3.Agent {
	return &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Name: compilerTestRunnableAgent, Namespace: harness.Namespace},
		Spec: v1alpha3.AgentSpec{Template: &template.Spec, Harness: &harness.Spec}}
}

func TestResolveModelConfigMistral(t *testing.T) {
	credentialField := env.MistralAPIKey.Name()
	model := &v1alpha3.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "mistral", Namespace: compilerTestNamespace},
		Spec: v1alpha3.ModelConfigSpec{Model: "mistral-large-latest", Provider: v1alpha3.ModelProviderMistral,
			APIKeySecret: "mistral-auth", APIKeySecretKey: credentialField},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "mistral-auth", Namespace: compilerTestNamespace}, Data: map[string][]byte{credentialField: []byte("key")}}
	resolved := mockCollections(t, model, secret).ResolvedModelConfigs.List()[0]
	require.True(t, resolved.Usable(), "%+v", resolved.Failure())

	resolved = mockCollections(t, model).ResolvedModelConfigs.List()[0]
	require.Equal(t, "APIKeySecretNotFound", resolved.Failure().Reason)
}

func TestCompileAgentTemplateKeepsSkillCredentialsOutOfConfig(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "skills-git-auth", Namespace: compilerTestNamespace, UID: types.UID("secret-uid")},
		Data:       map[string][]byte{compilerTestTokenField: []byte("ghp_private-skills-token")},
	}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:   &v1alpha3.KagentHarness{},
			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	credentialRef := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name}, Key: compilerTestTokenField}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName},
			Skills: []v1alpha3.AgentTemplateSkill{
				{Name: compilerTestRunbookSkill, Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
					URL: compilerTestPrivateSkillURL, Commit: compilerTestSkillCommit, CredentialRef: credentialRef,
				}, Path: compilerTestRunbookSkill}},
				{Name: "public", Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
					URL: "https://github.com/acme/public-skills", Commit: "dddddddddddddddddddddddddddddddddddddddd",
				}}},
			},
			Plugins: []v1alpha3.PluginBundle{{
				Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
					URL: "https://github.com/acme/private-plugins", Commit: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", CredentialRef: credentialRef,
				}},
				Skills: []string{"deploy"},
			}},
		},
	}
	spec, err := compiler(t, modelConfig(), secret).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.NoError(t, err)

	if bytes.Contains(spec.ConfigJSON, secret.Data[compilerTestTokenField]) || bytes.Contains(spec.Provenance, secret.Data[compilerTestTokenField]) {
		t.Fatal("runtime revision contains the skill credential value")
	}
	var config adk.AgentConfig
	require.NoError(t, json.Unmarshal(spec.ConfigJSON, &config))
	require.NotNil(t, config.AgentPlugins)
	require.Len(t, config.AgentPlugins.Skills, 2)
	require.Len(t, config.AgentPlugins.Plugins, 1)
	authenticated := map[string]bool{}
	for _, skill := range config.AgentPlugins.Skills {
		authenticated[skill.Source.Git.URL] = skill.Source.Git.Authenticated
	}
	authenticated[config.AgentPlugins.Plugins[0].Source.Git.URL] = config.AgentPlugins.Plugins[0].Source.Git.Authenticated
	require.Equal(t, map[string]bool{
		compilerTestPrivateSkillURL:               true,
		"https://github.com/acme/public-skills":   false,
		"https://github.com/acme/private-plugins": true,
	}, authenticated, "only a source with a credentialRef sends the placeholder the gateway replaces")

	var artifactCredentials []corev1.EnvVar
	for _, variable := range spec.Environment {
		require.Nil(t, variable.ValueFrom, "runtime revision environment contains unresolved valueFrom: %+v", variable)
		if strings.HasPrefix(variable.Name, translator.ArtifactCredentialEnvPrefix) {
			artifactCredentials = append(artifactCredentials, variable)
		}
	}
	require.Len(t, artifactCredentials, 1, "one Secret key compiles to one variable")
	require.Equal(t, translator.CredentialPlaceholder, artifactCredentials[0].Value, "the actor holds a placeholder, the gateway the credential")
	require.Contains(t, spec.Credentials, egress.Credential{
		Hostname: "github.com", Header: compilerTestLowerAuthorizationHeader, Prefix: "Basic ", URI: "ate-secret://k8s.io/default/test/" + secret.Name + "/token", Scope: egress.ScopeGolden,
	}, "credentials: %+v", spec.Credentials)
	require.Empty(t, slices.DeleteFunc(egress.ForActors(spec.Credentials), func(c egress.Credential) bool { return c.Hostname != "github.com" }),
		"a session's actor reaches the source's host without the credential")
	require.Zero(t, bytes.Count(spec.Provenance, []byte(`"kind":"Secret"`)), "a rotation of the credential is not a new revision; the gateway reads the Secret live: %s", spec.Provenance)
	require.Equal(t, []string{"http://kagent-controller.kagent:8083", "https://api.openai.com:443", "https://github.com:443"}, spec.EgressDestinations)
}

func TestCompileAgentTemplateReportsAMissingSkillCredentialSecret(t *testing.T) {
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:   &v1alpha3.KagentHarness{},
			Workload: v1alpha3.HarnessWorkload{Image: compilerTestKagentImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{
				WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
			},
		},
	}
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHelperName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName},
			Skills: []v1alpha3.AgentTemplateSkill{{Name: compilerTestRunbookSkill, Source: v1alpha3.ArtifactSource{Git: &v1alpha3.GitArtifact{
				URL: compilerTestPrivateSkillURL, Commit: compilerTestSkillCommit,
				CredentialRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: compilerTestMissingName}, Key: compilerTestTokenField},
			}}}},
		},
	}
	_, err := compiler(t, modelConfig()).CompileAgent(t.Context(), inlineAgent(harness, template))
	require.ErrorContains(t, err, `secret "missing" not found`)
	var validation *translator.ValidationError
	require.False(t, errors.As(err, &validation), "a missing Secret is a reference failure the controller retries, not a validation error")
}
func TestCompileAgentAddsTheAgentEgress(t *testing.T) {
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "go-coder", Namespace: compilerTestNamespace},
		Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: compilerTestPrompt},
	}
	harness := &v1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Name: compilerTestHarnessName, Namespace: compilerTestNamespace},
		Spec: v1alpha3.HarnessSpec{
			Kagent:    &v1alpha3.KagentHarness{},
			Workload:  v1alpha3.HarnessWorkload{Image: compilerTestRuntimeImage},
			Substrate: v1alpha3.RuntimeSubstratePolicy{WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestPoolName}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation}},
		},
	}
	declared := []string{"https://proxy.golang.org", "https://*.githubusercontent.com", "https://Proxy.Golang.org.", "https://git.internal:8443"}
	want := []string{"https://proxy.golang.org:443", "https://*.githubusercontent.com:443", "https://git.internal:8443"}
	c := compiler(t, modelConfig(), template, harness)

	referenced := &v1alpha3.Agent{ObjectMeta: metav1.ObjectMeta{Name: "coder", Namespace: compilerTestNamespace}, Spec: v1alpha3.AgentSpec{
		TemplateRef: &corev1.LocalObjectReference{Name: template.Name}, HarnessRef: &corev1.LocalObjectReference{Name: harness.Name}, Egress: declared,
	}}
	inline := inlineAgent(harness, template)
	inline.Spec.Egress = declared
	for name, agent := range map[string]*v1alpha3.Agent{compilerTestReferenceSchema: referenced, "inline": inline} {
		t.Run(name, func(t *testing.T) {
			result, err := c.CompileAgent(t.Context(), agent)
			require.NoError(t, err)
			require.Subset(t, result.EgressDestinations, want)
			require.Equal(t, 1, countOf(result.EgressDestinations, "https://proxy.golang.org:443"), "an origin is listed once")
			policy, err := substrate.ActorEgressPolicy(compilerTestNamespace, result.EgressDestinations, result.Credentials)
			require.NoError(t, err)
			var hostnames []string
			for _, rule := range policy.Rules {
				hostnames = append(hostnames, rule.GetHttps().GetHostnames()...)
			}
			require.Subset(t, hostnames, []string{"proxy.golang.org", "*.githubusercontent.com", "git.internal"})
		})
	}

	withEgress, err := c.CompileAgent(t.Context(), referenced)
	require.NoError(t, err)
	plain := referenced.DeepCopy()
	plain.Spec.Egress = nil
	withoutEgress, err := c.CompileAgent(t.Context(), plain)
	require.NoError(t, err)
	// The destinations are a digest input, so the revision identity follows them.
	require.Subset(t, withEgress.EgressDestinations, want)
	for _, origin := range want {
		require.NotContains(t, withoutEgress.EgressDestinations, origin)
	}

	invalid := referenced.DeepCopy()
	invalid.Spec.Egress = []string{"https://proxy.golang.org/path"}
	_, err = c.CompileAgent(t.Context(), invalid)
	var validation *translator.ValidationError
	require.ErrorAs(t, err, &validation)

	passthrough := referenced.DeepCopy()
	passthrough.Spec.Egress = []string{compilerTestTLSOrigin, "tls://*.kafka.example.com:9093"}
	result, err := c.CompileAgent(t.Context(), passthrough)
	require.NoError(t, err)
	require.Subset(t, result.EgressDestinations, passthrough.Spec.Egress)
	policy, err := substrate.ActorEgressPolicy(compilerTestNamespace, result.EgressDestinations, result.Credentials)
	require.NoError(t, err)
	var passthroughHosts []string
	for _, rule := range policy.Rules {
		passthroughHosts = append(passthroughHosts, rule.GetTlsPassthrough().GetHostnames()...)
	}
	require.ElementsMatch(t, []string{"db.example.com", "*.kafka.example.com"}, passthroughHosts, "one TLS passthrough rule per entry")
	require.Contains(t, result.EgressDestinations, "https://api.openai.com:443", "the model infers its HTTPS destination")

	for name, declared := range map[string][]string{
		"declared":               {"tls://api.example.com:443", "https://api.example.com"},
		"declared and HTTP":      {"http://db.example.com:5432", compilerTestTLSOrigin},
		"declared and its model": {"tls://api.openai.com:443"},
		"without port":           {"tls://db.example.com"},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			refused := referenced.DeepCopy()
			refused.Spec.Egress = declared
			_, err := c.CompileAgent(t.Context(), refused)
			require.ErrorAs(t, err, &validation)
		})
	}
}

func countOf(values []string, want string) int {
	n := 0
	for _, value := range values {
		if value == want {
			n++
		}
	}
	return n
}

func TestCompileAgentCarriesToolImages(t *testing.T) {
	const kubectlImage = "example.com/kubectl@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const helmImage = "example.com/helm@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	for _, harnessType := range []translator.HarnessType{
		translator.HarnessTypeKagent, translator.HarnessTypeCodex, translator.HarnessTypeClaude, translator.HarnessTypeBYO,
	} {
		t.Run(string(harnessType), func(t *testing.T) {
			harness := &v1alpha3.Harness{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: string(harnessType)},
				Spec: v1alpha3.HarnessSpec{
					Workload: v1alpha3.HarnessWorkload{Image: compilerTestAgentImage},
					Substrate: v1alpha3.RuntimeSubstratePolicy{
						WorkerPoolRef: corev1.LocalObjectReference{Name: compilerTestSelectedTool}, SnapshotPolicy: v1alpha3.RuntimeSnapshotPolicy{Location: compilerTestSnapshotLocation},
					},
				},
			}
			template := &v1alpha3.AgentTemplate{
				ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestAssistantName},
				Spec:       v1alpha3.AgentTemplateSpec{ModelConfig: &corev1.LocalObjectReference{Name: compilerTestModelName}, SystemPrompt: compilerTestPrompt},
			}
			model := modelConfig()
			model.Spec.APIKeySecret, model.Spec.APIKeySecretKey = compilerTestModelCredential, compilerTestAPIKeyField
			switch harnessType {
			case translator.HarnessTypeKagent:
				harness.Spec.Kagent = &v1alpha3.KagentHarness{}
			case translator.HarnessTypeCodex:
				harness.Spec.Codex = &v1alpha3.CodexHarness{}
				responses := v1alpha3.OpenAIAPIFormatResponses
				model.Spec.OpenAI = &v1alpha3.OpenAIConfig{APIFormat: &responses}
			case translator.HarnessTypeClaude:
				harness.Spec.Claude = &v1alpha3.ClaudeHarness{}
				model.Spec.Provider, model.Spec.Model = v1alpha3.ModelProviderAnthropic, compilerTestClaudeModel
			case translator.HarnessTypeBYO:
				harness.Spec.BYO = &v1alpha3.BYOHarness{}
				harness.Spec.Workload.Command = []string{"/agent"}
				template.Spec.ModelConfig = nil
			}
			objects := []any{
				model, harness,
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestModelCredential}, Data: map[string][]byte{compilerTestAPIKeyField: []byte("secret")}},
				&atev1alpha1.WorkerPool{ObjectMeta: metav1.ObjectMeta{Namespace: compilerTestNamespace, Name: compilerTestSelectedTool}},
			}
			plain, err := compiler(t, objects...).CompileAgent(t.Context(), inlineAgent(harness, template))
			require.NoError(t, err)
			require.Nil(t, plain.ToolImages, "a Harness without tool images compiles to a nil list")
			plainDigest, err := plain.Digest()
			require.NoError(t, err)

			harness.Spec.Workload.ToolImages = []v1alpha3.HarnessToolImage{{Name: "kubectl", OCI: kubectlImage}, {Name: "helm", OCI: helmImage}}
			originalHarness := harness.DeepCopy()
			result, err := compiler(t, objects...).CompileAgent(t.Context(), inlineAgent(harness, template))
			require.NoError(t, err)
			require.Equal(t, originalHarness, harness)
			require.Equal(t, []translator.ToolImage{{Name: "kubectl", Reference: kubectlImage}, {Name: "helm", Reference: helmImage}}, result.ToolImages)
			digest, err := result.Digest()
			require.NoError(t, err)
			require.NotEqual(t, plainDigest, digest, "tool images are part of revision identity")

			actor, err := substrate.ActorTemplateForRevision(&result.Revision, digest)
			require.NoError(t, err)
			var toolMounts []string
			for _, mount := range actor.GetContainers()[0].GetVolumeMounts() {
				if strings.HasPrefix(mount.GetMountPath(), "/opt/kagent/tools/") {
					toolMounts = append(toolMounts, mount.GetMountPath())
				}
			}
			require.Equal(t, []string{"/opt/kagent/tools/kubectl", "/opt/kagent/tools/helm"}, toolMounts, "after the runtime's own mounts, in the Harness's order")
		})
	}
}
