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
	// origins holds the canonical egress origins a tool image may name.
	origins      map[string]bool
	templates    toolsetTemplates
	pollInterval time.Duration
}

// WithToolImages admits session tool images from repositories, naming only
// egress origins among origins, preparing their runtimes through templates.
// An origin entry that does not parse admits nothing (ToolImageOrigins
// validates them). Without it every session tool image is refused.
func WithToolImages(repositories, origins []string, templates toolsetTemplates) Option {
	policy := newToolImages(repositories, origins, templates)
	return func(service *Service) { service.toolImages = policy }
}

func newToolImages(repositories, origins []string, templates toolsetTemplates) *toolImages {
	allowed := make(map[string]bool, len(origins))
	for _, entry := range origins {
		if origin, err := egress.ParseOrigin(entry); err == nil {
			allowed[origin] = true
		}
	}
	return &toolImages{repositories: repositories, origins: allowed, templates: templates, pollInterval: toolsetPollInterval}
}

// ToolImageOrigins validates the controller's tool image egress origins.
func ToolImageOrigins(entries []string) error {
	for _, entry := range entries {
		if _, err := egress.ParseOrigin(entry); err != nil {
			return fmt.Errorf("tool image egress origin %q: %w", entry, err)
		}
	}
	return nil
}

// sessionToolset validates a request's tool images against the controller's
// policy and returns them with their canonical origins.
func (t *toolImages) sessionToolset(images []*apiv1alpha1.SessionToolImage) (substrate.SessionToolset, error) {
	for _, tool := range images {
		if t == nil || !image.InRepositories(tool.GetOci(), t.repositories) {
			return substrate.SessionToolset{}, serviceerrors.NewInvalidArgument(fmt.Sprintf(
				"tool image %q comes from repository %q, which this controller does not allow for session tool images", tool.GetName(), image.Repository(tool.GetOci())), nil)
		}
		single, err := toolsetOf([]*apiv1alpha1.SessionToolImage{tool})
		if err != nil {
			return substrate.SessionToolset{}, err
		}
		for _, origin := range single.EgressDestinations {
			if !t.origins[origin] {
				return substrate.SessionToolset{}, serviceerrors.NewInvalidArgument(fmt.Sprintf(
					"tool image %q names egress origin %q, which this controller does not allow for session tool images", tool.GetName(), origin), nil)
			}
		}
	}
	return toolsetOf(images)
}

// toolsetOf returns a session's tool images with their canonical origins.
func toolsetOf(images []*apiv1alpha1.SessionToolImage) (substrate.SessionToolset, error) {
	var toolset substrate.SessionToolset
	for _, tool := range images {
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
	revision, desired, err := toolsetRevision(ctx, s.toolImages.templates, base, toolset)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, toolsetPreparationTimeout)
	defer cancel()
	observed, err := ensureToolsetTemplate(ctx, s.toolImages.templates, desired, s.toolImages.pollInterval)
	if err != nil {
		return nil, err
	}
	revision.ActorTemplateUID = observed.GetMetadata().GetUid()
	return revision, nil
}

// toolsetRevision derives the revision of base prepared with toolset and the
// ActorTemplate it runs on; the revision's template UID is the created
// template's, unknown here.
func toolsetRevision(ctx context.Context, templates toolsetTemplates, base *database.RuntimeRevision, toolset substrate.SessionToolset) (*database.RuntimeRevision, *ateapipb.ActorTemplate, error) {
	access, err := egress.CanonicalAccess(append(append([]string(nil), base.EgressDestinations...), toolset.EgressDestinations...), base.Credentials)
	if err != nil {
		return nil, nil, serviceerrors.NewInvalidArgument("tool image egress destinations conflict with the Agent's: "+err.Error(), nil)
	}
	revisionID, err := substrate.ToolsetRevisionID(base.Revision, toolset)
	if err != nil {
		return nil, nil, serviceerrors.NewInternal("Failed to identify the toolset revision", err)
	}
	baseTemplate, err := templates.GetActorTemplate(ctx, base.ActorTemplateAtespace, base.ActorTemplateName)
	if err != nil {
		return nil, nil, serviceerrors.NewUnavailable("Failed to read the Agent's runtime template", err)
	}
	if baseTemplate.GetMetadata().GetUid() != base.ActorTemplateUID {
		return nil, nil, serviceerrors.NewUnavailable("The Agent's runtime template changed; retry", nil)
	}
	desired, err := substrate.ActorTemplateWithTools(baseTemplate, base.AgentName, revisionID, toolset.Images)
	if err != nil {
		return nil, nil, serviceerrors.NewInvalidArgument(err.Error(), nil)
	}
	// Substrate gives a new actor its template's default egress policy, which
	// the session's own policy must match: compile it from the toolset's access.
	policy, err := substrate.ActorEgressPolicy(base.Namespace, access.Destinations, base.Credentials)
	if err != nil {
		return nil, nil, serviceerrors.NewInvalidArgument("tool image egress destinations: "+err.Error(), nil)
	}
	desired.DefaultEgressPolicy = &ateapipb.EgressPolicyTemplate{Rules: policy.GetRules()}
	revision := *base
	revision.Revision = revisionID.String()
	revision.EgressDestinations = access.Destinations
	revision.ActorTemplateName = desired.GetMetadata().GetName()
	revision.ActorTemplateUID = ""
	return &revision, desired, nil
}

// ensureToolsetTemplate creates the template unless it exists and returns it
// once its golden snapshot is ready, reading it every pollInterval. A zero
// pollInterval reads it once: a template still preparing returns nil.
func ensureToolsetTemplate(ctx context.Context, templates toolsetTemplates, desired *ateapipb.ActorTemplate, pollInterval time.Duration) (*ateapipb.ActorTemplate, error) {
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
			// The boot's message may quote configuration; it stays the cause.
			return nil, serviceerrors.NewFailedPrecondition("The session's tool images failed to start", errors.New(message))
		}
		if pollInterval == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, serviceerrors.NewUnavailable("The session's tool images are still being prepared; retry with the same request_id", ctx.Err())
		case <-time.After(pollInterval):
		}
		observed, err = templates.GetActorTemplate(ctx, atespace, name)
	}
}
