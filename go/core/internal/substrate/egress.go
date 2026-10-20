package substrate

import (
	"context"
	"fmt"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// EnsureActorEgressPolicy can be retried after a lost create response. A prepared
// revision is immutable, so an existing policy must match; never accept or
// overwrite a different allowlist, except inherited: the template's default a
// new Actor starts with, which is replaced before the Actor first runs.
// Substrate deletes the policy with its Actor.
func (c *Client) EnsureActorEgressPolicy(ctx context.Context, atespace, name string, policy, inherited *ateapipb.EgressPolicy) error {
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
	if inherited == nil || !SameRules(existing, inherited) {
		return fmt.Errorf("existing Actor egress policy does not match the prepared revision")
	}
	// The existing metadata carries the uid and version preconditions.
	replacement := &ateapipb.EgressPolicy{Metadata: existing.GetMetadata(), Rules: policy.GetRules()}
	_, err = c.UpdateActorEgressPolicy(ctx, &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: replacement})
	return err
}

// SameRules reports whether two egress policies hold the same rules, whatever
// their metadata.
func SameRules(a, b *ateapipb.EgressPolicy) bool {
	return proto.Equal(&ateapipb.EgressPolicy{Rules: a.GetRules()}, &ateapipb.EgressPolicy{Rules: b.GetRules()})
}

// ReplaceActorEgressPolicy gives an Actor the allowlist of the revision it is
// repointed to: it creates the policy, or replaces the rules of the one it has.
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
