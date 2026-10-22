package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/service/serviceerrors"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
)

const (
	toolsetGoImage   = "registry.example.com/tools/go@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	toolsetNodeImage = "registry.example.com/tools/node@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

// toolsetTestTemplates holds ActorTemplates by name. A created template turns
// ready after readyAfter reads, or reports failure when set.
type toolsetTestTemplates struct {
	mu         sync.Mutex
	templates  map[string]*ateapipb.ActorTemplate
	reads      map[string]int
	readyAfter int
	failure    string
	created    []*ateapipb.ActorTemplate
	createErr  error
}

func (t *toolsetTestTemplates) GetActorTemplate(_ context.Context, _, name string) (*ateapipb.ActorTemplate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	template, ok := t.templates[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	t.reads[name]++
	if template.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag() == nil && t.reads[name] > t.readyAfter {
		if t.failure != "" {
			template.Status = &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{ErrorMessage: t.failure}}
		} else {
			template.Status = &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{GoldenTag: &ateapipb.ObjectRef{Name: name + "-golden"}}}
		}
	}
	return proto.CloneOf(template), nil
}

func (t *toolsetTestTemplates) CreateActorTemplate(_ context.Context, template *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.createErr != nil {
		return nil, t.createErr
	}
	stored := proto.CloneOf(template)
	stored.Metadata.Uid = "uid-" + template.GetMetadata().GetName()
	t.templates[template.GetMetadata().GetName()] = stored
	t.created = append(t.created, proto.CloneOf(template))
	return proto.CloneOf(stored), nil
}

func toolsetTestBase() (*database.RuntimeRevision, *toolsetTestTemplates) {
	base := &ateapipb.ActorTemplate{
		Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "assistant-base", Uid: "base-uid"},
		Containers: []*ateapipb.Container{{
			Name: "kagent", Image: "runtime.example/kagent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Env:          []*ateapipb.EnvVar{{Name: "KAGENT_CONFIG_JSON", Value: "{}"}},
			VolumeMounts: []*ateapipb.VolumeMount{{Name: "data", MountPath: "/data"}},
		}},
		Volumes: []*ateapipb.Volume{{Name: "data", DurableDir: &ateapipb.DurableDirVolumeSource{}}},
		Status:  &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{GoldenTag: &ateapipb.ObjectRef{Name: "base-golden"}}},
	}
	revision := &database.RuntimeRevision{
		Revision: "base-revision", Namespace: "team-a", AgentName: "assistant", AgentUID: "agent-uid",
		EgressDestinations:    []string{"https://api.anthropic.com:443"},
		ActorTemplateAtespace: "team-a", ActorTemplateName: "assistant-base", ActorTemplateUID: "base-uid",
	}
	return revision, &toolsetTestTemplates{templates: map[string]*ateapipb.ActorTemplate{"assistant-base": base}, reads: map[string]int{}}
}

func toolsetTestService(t *testing.T, store *serviceTestStore, templates *toolsetTestTemplates) *Service {
	t.Helper()
	service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, WithToolImages([]string{"registry.example.com/tools"}, templates))
	service.toolImages.pollInterval = time.Millisecond
	return service
}

func toolsetTestImages() []*apiv1.SessionToolImage {
	return []*apiv1.SessionToolImage{
		{Name: "go", Oci: toolsetGoImage, EgressDestinations: []string{"https://proxy.golang.org", "https://sum.golang.org"}},
		{Name: "node", Oci: toolsetNodeImage, EgressDestinations: []string{"https://registry.npmjs.org"}},
	}
}

func TestServiceCreatePinsTheSessionToItsToolsetRevision(t *testing.T) {
	base, templates := toolsetTestBase()
	templates.readyAfter = 2
	store := &serviceTestStore{latest: base}
	service := toolsetTestService(t, store, templates)
	agent := &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}

	session, err := service.Create(serviceTestContext("alice"), agent, "request-1", "", toolsetTestImages())
	require.NoError(t, err)
	require.Len(t, templates.created, 1)
	created := templates.created[0]

	id, err := substrate.ToolsetRevisionID(base.Revision, substrate.SessionToolset{
		Images:             []translator.ToolImage{{Name: "go", Reference: toolsetGoImage}, {Name: "node", Reference: toolsetNodeImage}},
		EgressDestinations: []string{"https://proxy.golang.org:443", "https://sum.golang.org:443", "https://registry.npmjs.org:443"},
	})
	require.NoError(t, err)
	require.NotNil(t, store.toolset)
	require.Equal(t, id.String(), store.toolset.Revision)
	require.Equal(t, id.String(), session.GetPreparedRevision())
	require.Equal(t, created.GetMetadata().GetName(), store.toolset.ActorTemplateName)
	require.Equal(t, "uid-"+created.GetMetadata().GetName(), store.toolset.ActorTemplateUID)
	require.Equal(t, "team-a", store.toolset.ActorTemplateAtespace)
	require.Equal(t, base.AgentUID, store.toolset.AgentUID)
	require.ElementsMatch(t, []string{"https://api.anthropic.com:443", "https://proxy.golang.org:443", "https://sum.golang.org:443", "https://registry.npmjs.org:443"}, store.toolset.EgressDestinations)
	require.Equal(t, []string{"https://api.anthropic.com:443"}, base.EgressDestinations, "the base revision is not modified")
	require.Len(t, store.createInput.GetToolImages(), 2)
	require.Equal(t, "go", store.createInput.GetToolImages()[0].GetName())

	var mounts []string
	for _, mount := range created.GetContainers()[0].GetVolumeMounts() {
		mounts = append(mounts, mount.GetMountPath())
	}
	require.Equal(t, []string{"/data", "/opt/kagent/tools/go", "/opt/kagent/tools/node"}, mounts)

	// A second session with the same set reuses the prepared template.
	store.toolset = nil
	_, err = service.Create(serviceTestContext("bob"), agent, "request-2", "", toolsetTestImages())
	require.NoError(t, err)
	require.Len(t, templates.created, 1)
	require.Equal(t, id.String(), store.toolset.Revision)

	// Another version of a tool is another revision.
	other := toolsetTestImages()
	other[0].Oci = "registry.example.com/tools/go@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, err = service.Create(serviceTestContext("bob"), agent, "request-3", "", other)
	require.NoError(t, err)
	require.Len(t, templates.created, 2)
	require.NotEqual(t, id.String(), store.toolset.Revision)
}

func TestServiceCreateWithoutToolImagesUsesTheLatestRevision(t *testing.T) {
	base, templates := toolsetTestBase()
	store := &serviceTestStore{latest: base}
	service := toolsetTestService(t, store, templates)
	_, err := service.Create(serviceTestContext("alice"), &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, "request-1", "", nil)
	require.NoError(t, err)
	require.Nil(t, store.toolset)
	require.Empty(t, templates.created)
}

func TestServiceCreateRefusesToolImages(t *testing.T) {
	agent := &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}
	for _, test := range []struct {
		name    string
		service func(*serviceTestStore, *toolsetTestTemplates) *Service
		images  []*apiv1.SessionToolImage
		code    serviceerrors.Code
		message string
	}{
		{
			name:   "an unlisted repository",
			images: []*apiv1.SessionToolImage{{Name: "go", Oci: "registry.example.com/tools-extra/go@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}},
			code:   serviceerrors.CodeInvalidArgument, message: `repository "registry.example.com/tools-extra/go"`,
		},
		{
			name: "a controller that allows none",
			service: func(store *serviceTestStore, _ *toolsetTestTemplates) *Service {
				return NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{})
			},
			images: []*apiv1.SessionToolImage{{Name: "go", Oci: toolsetGoImage}},
			code:   serviceerrors.CodeInvalidArgument, message: "does not allow",
		},
		{
			name:   "an invalid origin",
			images: []*apiv1.SessionToolImage{{Name: "go", Oci: toolsetGoImage, EgressDestinations: []string{"https://proxy.golang.org/path"}}},
			code:   serviceerrors.CodeInvalidArgument, message: `tool image "go"`,
		},
		{
			name:   "a protocol conflict with the Agent's origins",
			images: []*apiv1.SessionToolImage{{Name: "go", Oci: toolsetGoImage, EgressDestinations: []string{"http://api.anthropic.com:443"}}},
			code:   serviceerrors.CodeInvalidArgument, message: "conflict with the Agent's",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, templates := toolsetTestBase()
			store := &serviceTestStore{latest: base}
			service := toolsetTestService(t, store, templates)
			if test.service != nil {
				service = test.service(store, templates)
			}
			_, err := service.Create(serviceTestContext("alice"), agent, "request-1", "", test.images)
			require.True(t, serviceerrors.IsCode(err, test.code), "error = %v", err)
			require.ErrorContains(t, err, test.message)
			require.Nil(t, store.createInput, "no session is reserved")
			require.Empty(t, templates.created)
		})
	}
}

func TestServiceCreateReportsToolsetPreparation(t *testing.T) {
	agent := &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}
	images := []*apiv1.SessionToolImage{{Name: "go", Oci: toolsetGoImage}}

	t.Run("an Agent without a ready revision", func(t *testing.T) {
		_, templates := toolsetTestBase()
		store := &serviceTestStore{}
		_, err := toolsetTestService(t, store, templates).Create(serviceTestContext("alice"), agent, "request-1", "", images)
		require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeFailedPrecondition), "error = %v", err)
	})

	t.Run("a golden boot that fails", func(t *testing.T) {
		base, templates := toolsetTestBase()
		templates.failure = "workload exited"
		store := &serviceTestStore{latest: base}
		_, err := toolsetTestService(t, store, templates).Create(serviceTestContext("alice"), agent, "request-1", "", images)
		require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeFailedPrecondition), "error = %v", err)
		require.ErrorContains(t, err, "workload exited")
		require.Nil(t, store.createInput)
	})

	t.Run("a deadline before the golden snapshot", func(t *testing.T) {
		base, templates := toolsetTestBase()
		templates.readyAfter = 1 << 30
		store := &serviceTestStore{latest: base}
		ctx, cancel := context.WithTimeout(serviceTestContext("alice"), 20*time.Millisecond)
		defer cancel()
		_, err := toolsetTestService(t, store, templates).Create(ctx, agent, "request-1", "", images)
		require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeUnavailable), "error = %v", err)
		require.ErrorContains(t, err, "retry with the same request_id")
		require.Nil(t, store.createInput)
	})

	t.Run("a base template replaced since the revision was recorded", func(t *testing.T) {
		base, templates := toolsetTestBase()
		base.ActorTemplateUID = "older-uid"
		store := &serviceTestStore{latest: base}
		_, err := toolsetTestService(t, store, templates).Create(serviceTestContext("alice"), agent, "request-1", "", images)
		require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeUnavailable), "error = %v", err)
		require.Empty(t, templates.created)
	})

	t.Run("a template Substrate rejects", func(t *testing.T) {
		base, templates := toolsetTestBase()
		templates.createErr = status.Error(codes.InvalidArgument, "secret detail")
		store := &serviceTestStore{latest: base}
		_, err := toolsetTestService(t, store, templates).Create(serviceTestContext("alice"), agent, "request-1", "", images)
		require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeInvalidArgument), "error = %v", err)
	})
}
