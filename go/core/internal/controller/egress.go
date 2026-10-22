package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
)

type actorEgressStore interface {
	ListRuntimeEgressSourceTargets(context.Context, database.RuntimeEgressSource, string, int) ([]database.RuntimeEgressTarget, error)
}

// actorEgressApplier gives one allocated instance the egress policy its
// source's grants make now. An instance whose Actor is gone or is not the one
// named has nothing to change.
type actorEgressApplier interface {
	ApplyEgress(context.Context, database.RuntimeEgressTarget) error
}

// applySourceEgress gives every allocated actor of a source the grants it holds
// now, suspended actors and the actors of former UIDs included, at most 16 at
// a time. It continues past an actor's failure, so one unavailable actor
// cannot hold the revocation of the others, and fails the preparation, which
// retries the whole source.
func applySourceEgress(ctx context.Context, store actorEgressStore, actors actorEgressApplier, source database.RuntimeEgressSource) error {
	const pageSize = 100
	var failures []error
	after := ""
	for {
		targets, err := store.ListRuntimeEgressSourceTargets(ctx, source, after, pageSize)
		if err != nil {
			return errors.Join(append(failures, fmt.Errorf("list the actors of %s %s/%s: %w", source.Kind, source.Namespace, source.Name, err))...)
		}
		var group errgroup.Group
		group.SetLimit(16)
		for _, target := range targets {
			group.Go(func() error {
				if err := actors.ApplyEgress(ctx, target); err != nil {
					return fmt.Errorf("apply the egress grants of instance %s: %w", target.ID, err)
				}
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			failures = append(failures, err)
		}
		if len(targets) < pageSize {
			return errors.Join(failures...)
		}
		after = targets[len(targets)-1].ID
	}
}

// admittedGrants is what a source's definition stores: the requested grants
// when they fit the runtime's pinned base, otherwise only the previously
// admitted ones still requested and still fitting. Removals apply at once;
// every addition waits until the conflict is resolved.
func admittedGrants(requested, previous, base egress.Access, caller *egress.CallerCredential, conflict string) egress.Access {
	if conflict == "" {
		return requested
	}
	kept, _ := egress.FilterGrants(base, egress.IntersectAccess(previous, requested), caller)
	return kept
}

// grantConflict reports why requested grants cannot all be admitted onto base,
// or "" when they can.
func grantConflict(requested, base egress.Access, caller *egress.CallerCredential) string {
	if _, err := egress.CanonicalAccess(requested.Destinations, requested.Credentials); err != nil {
		return err.Error()
	}
	if _, dropped := egress.FilterGrants(base, requested, caller); len(dropped) != 0 {
		return fmt.Sprintf("grants for %s conflict with the runtime's own access", strings.Join(dropped, ", "))
	}
	return ""
}

// egressPoliciesCondition reports grant admission, independently of readiness.
func egressPoliciesCondition(conditionType string, generation int64, issues []translator.EgressGrantIssue, conflict string) metav1.Condition {
	condition := metav1.Condition{
		Type: conditionType, ObservedGeneration: generation,
		Status: metav1.ConditionTrue, Reason: kagentv1alpha3.EgressPoliciesReasonApplied,
		Message: "All requested egress grants are admitted",
	}
	if len(issues) == 0 && conflict == "" {
		return condition
	}
	condition.Status = metav1.ConditionFalse
	var messages []string
	for _, issue := range issues {
		messages = append(messages, issue.Policy+": "+issue.Message)
	}
	if conflict != "" {
		condition.Reason = kagentv1alpha3.EgressPoliciesReasonConflict
		condition.Message = "new grants are held until the conflict is resolved; "
		messages = append(messages, "first conflict: "+conflict)
	} else {
		condition.Reason = issues[0].Reason
		condition.Message = ""
	}
	condition.Message += strings.Join(messages, "; ")
	// Stay below the Kubernetes condition message limit.
	const maxMessage = 4096
	if len(condition.Message) > maxMessage {
		condition.Message = strings.ToValidUTF8(condition.Message[:maxMessage-3], "") + "..."
	}
	return condition
}

// sandboxEgressClient reads and writes a sandbox Actor's egress policy.
type sandboxEgressClient interface {
	GetActor(context.Context, string, string) (*ateapipb.Actor, error)
	ApplyActorEgressPolicy(context.Context, string, string, func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)) error
}

type sandboxEgressStore interface {
	GetSandboxRevision(context.Context, string) (*database.SandboxRevision, error)
	GetRuntimeEgressGrant(context.Context, string) (egress.Access, error)
}

// sandboxEgress applies a SandboxTemplate's grants to its sandboxes' Actors.
type sandboxEgress struct {
	store  sandboxEgressStore
	actors sandboxEgressClient
}

func (s sandboxEgress) ApplyEgress(ctx context.Context, target database.RuntimeEgressTarget) error {
	binding := substrate.ActorBinding{Atespace: target.Atespace, Name: substrate.ActorName(target.ID), TemplateAtespace: target.Atespace, TemplateName: target.TemplateName}
	actor, err := s.actors.GetActor(ctx, binding.Atespace, binding.Name)
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if !binding.Matches(actor) {
		return nil
	}
	return s.actors.ApplyActorEgressPolicy(ctx, binding.Atespace, binding.Name, func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
		revision, err := s.store.GetSandboxRevision(ctx, target.Revision)
		if err != nil {
			return nil, err
		}
		grants, err := s.store.GetRuntimeEgressGrant(ctx, target.Revision)
		if err != nil {
			return nil, err
		}
		return substrate.SandboxEgressPolicy(binding.Atespace, egress.Access{Destinations: revision.EgressDestinations, Credentials: revision.Credentials}, grants)
	})
}
