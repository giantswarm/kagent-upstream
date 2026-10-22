package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/image"
	"github.com/kagent-dev/kagent/go/core/internal/service/serviceerrors"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
)

const (
	// toolsetPreparationTimeout bounds one call's wait for a new toolset's
	// golden snapshot. A retry with the same request_id continues the wait.
	toolsetPreparationTimeout = 5 * time.Minute
	toolsetPollInterval       = time.Second
)

// toolsetTemplates prepares the ActorTemplate of a toolset revision.
type toolsetTemplates interface {
	GetActorTemplate(context.Context, string, string) (*ateapipb.ActorTemplate, error)
	CreateActorTemplate(context.Context, *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error)
}

// toolImages is the controller's policy for session tool images.
type toolImages struct {
	repositories []string
	templates    toolsetTemplates
	pollInterval time.Duration
}

// WithToolImages admits session tool images from repositories, preparing their
// runtimes through templates. Without it every session tool image is refused.
func WithToolImages(repositories []string, templates toolsetTemplates) Option {
	return func(service *Service) {
		service.toolImages = &toolImages{repositories: repositories, templates: templates, pollInterval: toolsetPollInterval}
	}
}

// sessionToolset validates a request's tool images against the controller's
// policy and returns them with their canonical origins.
func (t *toolImages) sessionToolset(images []*apiv1alpha1.SessionToolImage) (substrate.SessionToolset, error) {
	var toolset substrate.SessionToolset
	for _, tool := range images {
		if t == nil || !image.InRepositories(tool.GetOci(), t.repositories) {
			return substrate.SessionToolset{}, serviceerrors.NewInvalidArgument(fmt.Sprintf(
				"tool image %q comes from repository %q, which this controller does not allow for session tool images", tool.GetName(), image.Repository(tool.GetOci())), nil)
		}
		toolset.Images = append(toolset.Images, translator.ToolImage{Name: tool.GetName(), Reference: tool.GetOci()})
		for _, destination := range tool.GetEgressDestinations() {
			origin, err := egress.ParseOrigin(destination)
			if err != nil {
				return substrate.SessionToolset{}, serviceerrors.NewInvalidArgument(fmt.Sprintf("tool image %q: %v", tool.GetName(), err), nil)
			}
			toolset.EgressDestinations = append(toolset.EgressDestinations, origin)
		}
	}
	return toolset, nil
}

// prepareToolset returns the revision a session with toolset runs: the
// Agent's latest successful revision with the tool images mounted and their
// origins added to its egress. It creates the revision's ActorTemplate when no
// earlier session prepared it and waits for its golden snapshot. The store
// records the revision with the session that references it.
func (s *Service) prepareToolset(ctx context.Context, agent *apiv1alpha1.ResourceReference, toolset substrate.SessionToolset) (*database.RuntimeRevision, error) {
	base, err := s.store.GetLatestAgentRevision(ctx, agent.GetNamespace(), agent.GetName())
	if errors.Is(err, database.ErrNotFound) {
		return nil, serviceerrors.NewFailedPrecondition("Agent does not have a ready prepared revision", err)
	}
	if err != nil {
		return nil, serviceerrors.NewInternal("Failed to read the Agent's prepared revision", err)
	}
	access, err := egress.CanonicalAccess(append(append([]string(nil), base.EgressDestinations...), toolset.EgressDestinations...), base.Credentials)
	if err != nil {
		return nil, serviceerrors.NewInvalidArgument("tool image egress destinations conflict with the Agent's: "+err.Error(), nil)
	}
	revisionID, err := substrate.ToolsetRevisionID(base.Revision, toolset)
	if err != nil {
		return nil, serviceerrors.NewInternal("Failed to identify the toolset revision", err)
	}
	templates := s.toolImages.templates
	baseTemplate, err := templates.GetActorTemplate(ctx, base.ActorTemplateAtespace, base.ActorTemplateName)
	if err != nil {
		return nil, serviceerrors.NewUnavailable("Failed to read the Agent's runtime template", err)
	}
	if baseTemplate.GetMetadata().GetUid() != base.ActorTemplateUID {
		return nil, serviceerrors.NewUnavailable("The Agent's runtime template changed; retry", nil)
	}
	desired, err := substrate.ActorTemplateWithTools(baseTemplate, base.AgentName, revisionID, toolset.Images)
	if err != nil {
		return nil, serviceerrors.NewInvalidArgument(err.Error(), nil)
	}
	ctx, cancel := context.WithTimeout(ctx, toolsetPreparationTimeout)
	defer cancel()
	observed, err := s.ensureToolsetTemplate(ctx, desired)
	if err != nil {
		return nil, err
	}
	revision := *base
	revision.Revision = revisionID.String()
	revision.EgressDestinations = access.Destinations
	revision.ActorTemplateName = observed.GetMetadata().GetName()
	revision.ActorTemplateUID = observed.GetMetadata().GetUid()
	return &revision, nil
}

// ensureToolsetTemplate creates the template unless it exists and returns it
// once its golden snapshot is ready.
func (s *Service) ensureToolsetTemplate(ctx context.Context, desired *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error) {
	templates := s.toolImages.templates
	atespace, name := desired.GetMetadata().GetAtespace(), desired.GetMetadata().GetName()
	observed, err := templates.GetActorTemplate(ctx, atespace, name)
	if status.Code(err) == codes.NotFound {
		observed, err = templates.CreateActorTemplate(ctx, desired)
		if status.Code(err) == codes.AlreadyExists {
			observed, err = templates.GetActorTemplate(ctx, atespace, name)
		}
	}
	if status.Code(err) == codes.InvalidArgument {
		// Backend messages may quote configuration; keep the response safe.
		return nil, serviceerrors.NewInvalidArgument("Substrate rejected the session's tool images (InvalidArgument)", err)
	}
	for {
		if err != nil {
			return nil, serviceerrors.NewUnavailable("Failed to prepare the session's tool images", err)
		}
		if !substrate.ActorTemplateSpecEqual(observed, desired) {
			return nil, serviceerrors.NewInternal("An existing runtime template differs from the session's toolset", nil)
		}
		golden := observed.GetStatus().GetGoldenSnapshotStatus()
		if golden.GetGoldenTag() != nil {
			return observed, nil
		}
		if message := golden.GetErrorMessage(); message != "" {
			return nil, serviceerrors.NewFailedPrecondition("The session's tool images failed to start: "+message, nil)
		}
		select {
		case <-ctx.Done():
			return nil, serviceerrors.NewUnavailable("The session's tool images are still being prepared; retry with the same request_id", ctx.Err())
		case <-time.After(s.toolImages.pollInterval):
		}
		observed, err = templates.GetActorTemplate(ctx, atespace, name)
	}
}
