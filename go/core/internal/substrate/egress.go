package substrate

import (
	"context"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// EnsureActorEgressPolicy gives a new Actor its allowlist: it creates the
// policy, or replaces the rules of the one the Actor holds, the default its
// template gave it or the one a lost response already wrote. A retry is
// idempotent. The policy is not immutable: the EgressPolicy grants of the
// Actor's source change it while it lives (ApplyActorEgressPolicy). Substrate
// deletes the policy with its Actor.
func (c *Client) EnsureActorEgressPolicy(ctx context.Context, atespace, name string, policy *ateapipb.EgressPolicy) error {
	return c.ReplaceActorEgressPolicy(ctx, atespace, name, policy)
}

// SameRules reports whether two egress policies hold the same rules, whatever
// their metadata.
func SameRules(a, b *ateapipb.EgressPolicy) bool {
	return proto.Equal(&ateapipb.EgressPolicy{Rules: a.GetRules()}, &ateapipb.EgressPolicy{Rules: b.GetRules()})
}

// ReplaceActorEgressPolicy gives an Actor policy's rules: it creates the
// policy, or replaces the rules of the one it has.
func (c *Client) ReplaceActorEgressPolicy(ctx context.Context, atespace, name string, policy *ateapipb.EgressPolicy) error {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	actor := actorRef(atespace, name)
	_, err := c.CreateActorEgressPolicy(ctx, &ateapipb.CreateActorEgressPolicyRequest{
		Actor: actor, EgressPolicy: policy,
	})
	if status.Code(err) != codes.AlreadyExists {
		return err
	}
	existing, err := c.GetActorEgressPolicy(ctx, &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		return err
	}
	if SameRules(existing, policy) {
		return nil
	}
	// The existing metadata carries the uid and version preconditions.
	replacement := &ateapipb.EgressPolicy{Metadata: existing.GetMetadata(), Rules: policy.GetRules()}
	_, err = c.UpdateActorEgressPolicy(ctx, &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: replacement})
	return err
}

// ApplyActorEgressPolicy gives an existing Actor the policy desired computes
// from the rules it holds now, behind their version: a write that lands
// between the read and the update answers Aborted, and the read, desired and
// the update run again. desired reads the intended access after the Actor's
// policy, so of two concurrent applications the later read wins. An Actor
// without a policy, or gone, has none to change: its creation writes one.
func (c *Client) ApplyActorEgressPolicy(ctx context.Context, atespace, name string, desired func(existing *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)) error {
	for range 5 {
		existing, err := c.ReadActorEgressPolicy(ctx, atespace, name)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		policy, err := desired(existing)
		if err != nil {
			return err
		}
		if SameRules(existing, policy) {
			return nil
		}
		_, err = c.UpdateActorEgressPolicyAt(ctx, atespace, name, existing, policy)
		if status.Code(err) == codes.Aborted {
			continue
		}
		return err
	}
	return status.Errorf(codes.Aborted, "the egress policy of Actor %s/%s kept changing while it was applied", atespace, name)
}

// ReadActorEgressPolicy reads an Actor's egress policy. Its metadata is the fence
// a later UpdateActorEgressPolicyAt writes behind.
func (c *Client) ReadActorEgressPolicy(ctx context.Context, atespace, name string) (*ateapipb.EgressPolicy, error) {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.GetActorEgressPolicy(ctx, &ateapipb.GetActorEgressPolicyRequest{Actor: actorRef(atespace, name)})
}

// UpdateActorEgressPolicyAt gives an Actor policy's rules while its egress
// policy is still the one fence read, and returns the policy written, the
// fence of a later write: a write that landed since answers Aborted and keeps
// its rules.
func (c *Client) UpdateActorEgressPolicyAt(ctx context.Context, atespace, name string, fence, policy *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.UpdateActorEgressPolicy(ctx, &ateapipb.UpdateActorEgressPolicyRequest{
		Actor: actorRef(atespace, name), EgressPolicy: &ateapipb.EgressPolicy{Metadata: fence.GetMetadata(), Rules: policy.GetRules()},
	})
}

// WriteActorEgressPolicy gives an Actor policy's rules whatever its egress
// policy holds, and returns the policy written. It always writes: the new
// version fences off every UpdateActorEgressPolicyAt behind an older read, one
// of the same rules too.
func (c *Client) WriteActorEgressPolicy(ctx context.Context, atespace, name string, policy *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
	for attempt := 1; ; attempt++ {
		fence, err := c.ReadActorEgressPolicy(ctx, atespace, name)
		if err != nil {
			return nil, err
		}
		written, err := c.UpdateActorEgressPolicyAt(ctx, atespace, name, fence, policy)
		if status.Code(err) != codes.Aborted || attempt == 3 {
			return written, err
		}
	}
}
