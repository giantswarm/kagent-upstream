package substrate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"

	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/kagent-dev/kagent/go/core/internal/image"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
	"github.com/kagent-dev/kagent/go/pkg/toolpath"
	"github.com/kagent-dev/kagent/go/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	workerPoolNameLabelKey      = "kagent.dev/worker-pool-name"
	workerPoolNamespaceLabelKey = "kagent.dev/worker-pool-namespace"
	defaultContainerName        = "kagent"
	durableDataVolume           = "data"
	durableDataMount            = "/data"
	// Matches EnvVar.value's maxLength in Substrate's ateapi.proto.
	maxEnvironmentValueRunes = 32768
)

const egressTrustVolume = "egress-trust"
const egressTrustMount = "/run/kagent/egress"

// egressTrustBundles are the Substrate trust bundles unified into the egress
// trust file. The gateway CA verifies intercepted HTTPS; the public roots
// verify a TLS passthrough destination, which presents its own chain.
var egressTrustBundles = []string{"egress-mitm.ate.dev", "system-roots.ate.dev"}

// egressTrustVolumeSource projects egressTrustBundles to trust-bundle.pem.
func egressTrustVolumeSource() *ateapipb.SystemInfoVolumeSource {
	return &ateapipb.SystemInfoVolumeSource{DataSources: []*ateapipb.SystemInfoDataSource{
		{TrustBundle: &ateapipb.TrustBundleDataSource{Names: append([]string(nil), egressTrustBundles...), Path: "trust-bundle.pem"}},
	}}
}

const actorIdentityVolume = "actor-identity"
const actorIdentityMount = "/run/kagent/identity"

// Tool image volumes carry a prefix so a tool named like a built-in volume
// cannot collide with it.
const toolImageVolumePrefix = "tool-"

var egressTrustEnvironment = map[string]struct{}{
	"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}, "REQUESTS_CA_BUNDLE": {}, "AWS_CA_BUNDLE": {},
	"NODE_EXTRA_CA_CERTS": {}, "CURL_CA_BUNDLE": {}, "GIT_SSL_CAINFO": {},
}

// ActorTemplateForRevision constructs the immutable ate-api resource for a
// compiled revision. It performs no reads or writes, which makes it safe to use
// inside a KRT transformation.
func ActorTemplateForRevision(spec *translator.Revision, revisionID translator.RevisionID) (*ateapipb.ActorTemplate, error) {
	if revisionID.IsZero() {
		return nil, fmt.Errorf("runtime revision ID is required")
	}
	workerKey := types.NamespacedName{Namespace: spec.Namespace, Name: spec.WorkerPoolName}
	name := revisionActorTemplateName(spec.AgentName, revisionID)
	// Config and SDK placeholders contain no Secret values. Render the typed
	// card only at this boundary.
	card, err := apia2a.FromProtoAgentCard(spec.AgentCard)
	if err != nil {
		return nil, fmt.Errorf("convert runtime Agent Card: %w", err)
	}
	cardJSON, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("render runtime Agent Card: %w", err)
	}
	environment := withServiceVersion(append([]corev1.EnvVar(nil), spec.Environment...), revisionID.Short())
	// The systemInfo trustBundle volume below projects the gateway CA and the
	// public roots. These variables tell each TLS client to trust them; mounting the file alone does
	// not configure trust. The gateway intercepts HTTPS even without credentials.
	for _, variable := range environment {
		if _, owned := egressTrustEnvironment[variable.Name]; owned {
			return nil, fmt.Errorf("runtime environment %q conflicts with gateway trust", variable.Name)
		}
		if variable.Name == toolpath.EnvName {
			return nil, fmt.Errorf("runtime environment %q is reserved for tool images", variable.Name)
		}
	}
	for _, name := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "AWS_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		environment = append(environment, corev1.EnvVar{Name: name, Value: egressTrustMount + "/trust-bundle.pem"})
	}
	environment = append(environment, corev1.EnvVar{Name: "SSL_CERT_DIR", Value: egressTrustMount})
	toolVolumes, toolMounts, toolNames, err := toolImageVolumes(spec.ToolImages)
	if err != nil {
		return nil, err
	}
	if len(toolNames) > 0 {
		environment = append(environment, corev1.EnvVar{Name: toolpath.EnvName, Value: toolpath.JoinBinDirs(toolNames)})
	}
	environment = append(environment,
		corev1.EnvVar{Name: "KAGENT_CONFIG_JSON", Value: string(spec.ConfigJSON)},
		corev1.EnvVar{Name: "KAGENT_AGENT_CARD_JSON", Value: string(cardJSON)},
	)
	actorEnv, err := actorTemplateEnvFromPodEnv(environment)
	if err != nil {
		return nil, err
	}
	if len(actorEnv) > 32 {
		return nil, fmt.Errorf("runtime revision has %d environment variables; Substrate supports at most 32", len(actorEnv))
	}
	sandboxConfig, err := sandboxConfigForClass(spec.SandboxClass)
	if err != nil {
		return nil, err
	}
	// Substrate copies the template's default policy onto every Actor it
	// creates from it, including the golden Actor, which boots before any
	// client call could give it a policy of its own.
	egressPolicy, err := ActorEgressPolicy(spec.Namespace, spec.EgressDestinations, spec.Credentials)
	if err != nil {
		return nil, fmt.Errorf("compile egress policy: %w", err)
	}

	var securityContext *ateapipb.SecurityContext
	if len(spec.Capabilities) != 0 {
		securityContext = &ateapipb.SecurityContext{Capabilities: &ateapipb.Capabilities{Add: append([]string(nil), spec.Capabilities...)}}
	}
	template := &ateapipb.ActorTemplate{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: spec.Namespace, Name: name},
		SandboxConfig: sandboxConfig,
		Containers: []*ateapipb.Container{{
			Name:    defaultContainerName,
			Image:   spec.Image,
			Command: append([]string(nil), spec.Command...),
			Args:    append([]string(nil), spec.Args...),
			Env:     actorEnv,
			WakeupProbe: &ateapipb.ContainerWakeupProbe{HttpGet: &ateapipb.HTTPGetAction{
				Path: "/readyz",
				Port: 8081,
			}, TimeoutSeconds: 30},
			VolumeMounts: append([]*ateapipb.VolumeMount{
				{Name: durableDataVolume, MountPath: durableDataMount},
				{Name: egressTrustVolume, MountPath: egressTrustMount},
				{Name: actorIdentityVolume, MountPath: actorIdentityMount},
			}, append(workspaceVolumeMounts(), toolMounts...)...),
			SecurityContext: securityContext,
		}},
		WorkerSelector:      workerSelectorForPool(workerKey),
		DefaultEgressPolicy: &ateapipb.EgressPolicyTemplate{Rules: egressPolicy.GetRules()},
		SnapshotConfig: &ateapipb.SnapshotConfig{
			StorageLocation: spec.SnapshotLocation,
			// Substrate 0.5 applies one scope to pause and suspend. Keep process memory
			// for both until lifecycle v2 separates them again.
			OnCommit: ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL,
		},
		Volumes: append([]*ateapipb.Volume{
			{Name: durableDataVolume, DurableDir: &ateapipb.DurableDirVolumeSource{}},
			// Substrate regenerates this projection on Run and Restore. A fork
			// therefore routes storage calls as its own actor, never its source.
			{Name: actorIdentityVolume, SystemInfo: &ateapipb.SystemInfoVolumeSource{DataSources: []*ateapipb.SystemInfoDataSource{
				{ActorMetadata: &ateapipb.ActorMetadataDataSource{Items: []*ateapipb.ActorMetadataItem{
					{Field: ateapipb.ActorMetadataField_ACTOR_METADATA_FIELD_NAME, Path: "name"},
					{Field: ateapipb.ActorMetadataField_ACTOR_METADATA_FIELD_ATESPACE, Path: "atespace"},
					{Field: ateapipb.ActorMetadataField_ACTOR_METADATA_FIELD_UID, Path: "uid"},
				}}},
			}}},
			{Name: egressTrustVolume, SystemInfo: egressTrustVolumeSource()},
			// A Session with a volume source supplies these at create
			// (SessionExistingVolumes); every other actor of the template,
			// the golden one included, gets neither volume nor mount.
		}, append(workspaceVolumes(), toolVolumes...)...),
	}
	return template, nil
}

// toolImageVolumes projects each tool image as a read-only image volume mounted
// under toolpath.MountRoot. The CRD validates the entries; the check here keeps
// a hand-built revision on the same contract Substrate enforces.
func toolImageVolumes(images []translator.ToolImage) ([]*ateapipb.Volume, []*ateapipb.VolumeMount, []string, error) {
	var volumes []*ateapipb.Volume
	var mounts []*ateapipb.VolumeMount
	var names []string
	seen := make(map[string]struct{}, len(images))
	for _, tool := range images {
		if errs := validation.IsDNS1123Label(tool.Name); len(errs) > 0 {
			return nil, nil, nil, fmt.Errorf("tool image name %q: %s", tool.Name, strings.Join(errs, "; "))
		}
		if _, duplicate := seen[tool.Name]; duplicate {
			return nil, nil, nil, fmt.Errorf("tool image name %q is listed twice", tool.Name)
		}
		seen[tool.Name] = struct{}{}
		if !image.IsDigestPinned(tool.Reference) {
			return nil, nil, nil, fmt.Errorf("tool image %q must be pinned by sha256 digest", tool.Name)
		}
		volumeName := toolImageVolumePrefix + tool.Name
		volumes = append(volumes, &ateapipb.Volume{Name: volumeName, Image: &ateapipb.ImageVolumeSource{Reference: tool.Reference}})
		mounts = append(mounts, &ateapipb.VolumeMount{Name: volumeName, MountPath: toolpath.MountPath(tool.Name)})
		names = append(names, tool.Name)
	}
	return volumes, mounts, names, nil
}

// SessionToolset is what a session adds to its Agent revision's runtime: tool
// images mounted beside the Harness's and the origins those tools reach.
type SessionToolset struct {
	Images             []translator.ToolImage
	EgressDestinations []string
}

// ToolsetRevisionID identifies an Agent revision prepared with a session
// toolset. Sessions of one revision that name the same images and origins, in
// any order, share it and its ActorTemplate.
func ToolsetRevisionID(baseRevision string, toolset SessionToolset) (translator.RevisionID, error) {
	if baseRevision == "" || len(toolset.Images) == 0 {
		return translator.RevisionID{}, fmt.Errorf("a toolset revision requires a base revision and at least one tool image")
	}
	images := slices.Clone(toolset.Images)
	slices.SortFunc(images, func(a, b translator.ToolImage) int { return strings.Compare(a.Name, b.Name) })
	destinations := slices.Clone(toolset.EgressDestinations)
	slices.Sort(destinations)
	raw, err := json.Marshal(struct {
		Base               string                 `json:"base"`
		Images             []translator.ToolImage `json:"images"`
		EgressDestinations []string               `json:"egressDestinations,omitempty"`
	}{Base: baseRevision, Images: images, EgressDestinations: slices.Compact(destinations)})
	if err != nil {
		return translator.RevisionID{}, fmt.Errorf("encode toolset revision: %w", err)
	}
	return sha256.Sum256(raw), nil
}

// ActorTemplateWithTools derives the template of a toolset revision from its
// base revision's template: the same runtime with the session's tool images
// mounted after the Harness's, their bin directories appended to
// KAGENT_TOOL_PATHS. Server-owned metadata and status are not copied.
func ActorTemplateWithTools(base *ateapipb.ActorTemplate, agentName string, revisionID translator.RevisionID, images []translator.ToolImage) (*ateapipb.ActorTemplate, error) {
	if revisionID.IsZero() {
		return nil, fmt.Errorf("runtime revision ID is required")
	}
	if len(base.GetContainers()) != 1 {
		return nil, fmt.Errorf("base ActorTemplate has %d containers; expected the runtime container alone", len(base.GetContainers()))
	}
	volumes, mounts, names, err := toolImageVolumes(images)
	if err != nil {
		return nil, err
	}
	template := proto.CloneOf(actorTemplateSpec(base))
	template.Metadata.Name = revisionActorTemplateName(agentName, revisionID)
	container := template.Containers[0]
	for _, volume := range volumes {
		if slices.ContainsFunc(template.Volumes, func(existing *ateapipb.Volume) bool { return existing.GetName() == volume.GetName() }) {
			return nil, fmt.Errorf("tool image %q is already mounted by the Harness", strings.TrimPrefix(volume.GetName(), toolImageVolumePrefix))
		}
	}
	template.Volumes = append(template.Volumes, volumes...)
	container.VolumeMounts = append(container.VolumeMounts, mounts...)
	toolPaths := toolpath.JoinBinDirs(names)
	if index := slices.IndexFunc(container.Env, func(variable *ateapipb.EnvVar) bool { return variable.GetName() == toolpath.EnvName }); index >= 0 {
		container.Env[index].Value = toolpath.ExtendPath(container.Env[index].GetValue(), toolPaths)
	} else {
		container.Env = append(container.Env, &ateapipb.EnvVar{Name: toolpath.EnvName, Value: toolPaths})
	}
	if len(container.Env) > 32 {
		return nil, fmt.Errorf("runtime revision has %d environment variables with its tool images; Substrate supports at most 32", len(container.Env))
	}
	if len(template.Volumes) > 32 || len(container.VolumeMounts) > 32 {
		return nil, fmt.Errorf("runtime revision has %d volumes and %d container mounts with its tool images; Substrate supports at most 32 volumes and 32 mounts per container", len(template.Volumes), len(container.VolumeMounts))
	}
	return template, nil
}

// ActorTemplateSpecEqual compares the client-owned immutable fields of two
// templates, excluding server-owned metadata and golden-snapshot status.
func ActorTemplateSpecEqual(left, right *ateapipb.ActorTemplate) bool {
	return proto.Equal(actorTemplateSpec(left), actorTemplateSpec(right))
}

func actorTemplateSpec(template *ateapipb.ActorTemplate) *ateapipb.ActorTemplate {
	if template == nil {
		return nil
	}
	return &ateapipb.ActorTemplate{
		Metadata: &ateapipb.ResourceMetadata{
			Atespace: template.GetMetadata().GetAtespace(),
			Name:     template.GetMetadata().GetName(),
		},
		WorkerSelector:      template.GetWorkerSelector(),
		Containers:          template.GetContainers(),
		Volumes:             template.GetVolumes(),
		SnapshotConfig:      template.GetSnapshotConfig(),
		SandboxConfig:       template.GetSandboxConfig(),
		Resources:           template.GetResources(),
		DefaultEgressPolicy: template.GetDefaultEgressPolicy(),
	}
}

// withServiceVersion stamps the revision on the runtime resource. The revision
// digests the environment, so the compiler cannot render it.
func withServiceVersion(environment []corev1.EnvVar, version string) []corev1.EnvVar {
	for index, variable := range environment {
		if variable.Name == tracing.ResourceEnvironmentVariable && variable.ValueFrom == nil {
			environment[index].Value = tracing.MergeResourceAttributes(variable.Value, []attribute.KeyValue{semconv.ServiceVersion(version)})
		}
	}
	return environment
}

func revisionActorTemplateName(agentName string, revision translator.RevisionID) string {
	// Twelve digest characters keep names readable while the full digest remains
	// the database identity and immutable-content check.
	base := truncateDNS1123(agentName)
	base = truncateDNS1123To(base, 50)
	return base + "-" + revision.Short()
}

func workerSelectorForPool(pool types.NamespacedName) *ateapipb.Selector {
	// Substrate matches worker labels across the entire fleet and never checks
	// them against the pool's namespace. Placement therefore stays within the
	// referenced pool's namespace only while every pool's labels name its own.
	return &ateapipb.Selector{MatchLabels: map[string]string{
		workerPoolNameLabelKey:      pool.Name,
		workerPoolNamespaceLabelKey: pool.Namespace,
	}}
}

func truncateDNS1123(value string) string {
	return truncateDNS1123To(value, 63)
}

func truncateDNS1123To(value string, limit int) string {
	value = strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	if len(value) > limit {
		value = strings.TrimRight(value[:limit], "-")
	}
	return value
}

func actorTemplateEnvFromPodEnv(environment []corev1.EnvVar) ([]*ateapipb.EnvVar, error) {
	// Only non-secret literals and SDK placeholders may cross this boundary.
	result := make([]*ateapipb.EnvVar, 0, len(environment))
	seen := make(map[string]struct{}, len(environment))
	for _, value := range environment {
		if value.Name == "" {
			continue
		}
		if value.ValueFrom != nil {
			return nil, fmt.Errorf("runtime environment variable %q is not resolved to a literal value", value.Name)
		}
		if _, exists := seen[value.Name]; exists {
			continue
		}
		if size := utf8.RuneCountInString(value.Value); size > maxEnvironmentValueRunes {
			return nil, fmt.Errorf("environment variable %q is %d characters; Substrate supports at most %d", value.Name, size, maxEnvironmentValueRunes)
		}
		seen[value.Name] = struct{}{}
		result = append(result, &ateapipb.EnvVar{Name: value.Name, Value: value.Value})
	}
	return result, nil
}

func sandboxConfigForClass(class atev1alpha1.SandboxClass) (*ateapipb.SandboxConfig, error) {
	switch class {
	case "", atev1alpha1.SandboxClassGvisor:
		return &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_GVISOR,
			ConfigName:   "gvisor-default",
		}, nil
	case atev1alpha1.SandboxClassMicroVM:
		return &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_MICROVM,
			ConfigName:   "microvm",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported sandbox class %q", class)
	}
}
