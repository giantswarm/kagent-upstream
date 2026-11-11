package a2a

import (
	"math"
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/require"
)

func TestTurnCostUSDOf(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]any
		cost     float64
		reported bool
		ok       bool
	}{
		{name: "no usage", ok: true},
		{name: "usage without a cost", metadata: map[string]any{UsageMetadataKey: map[string]any{"totalTokenCount": 10.0}}, ok: true},
		{name: "a cost", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": 0.42}}, cost: 0.42, reported: true, ok: true},
		{name: "a zero cost", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": 0.0}}, reported: true, ok: true},
		{name: "usage not an object", metadata: map[string]any{UsageMetadataKey: "1"}},
		{name: "a cost not a number", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": "0.42"}}},
		{name: "a negative cost", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": -0.1}}},
		{name: "an infinite cost", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": math.Inf(1)}}},
		{name: "a cost not a number at all", metadata: map[string]any{UsageMetadataKey: map[string]any{"costUsd": math.NaN()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := a2atype.NewMessage(a2atype.MessageRoleAgent)
			message.Metadata = tc.metadata
			cost, reported, ok := TurnCostUSDOf(message)
			require.Equal(t, tc.cost, cost)
			require.Equal(t, tc.reported, reported)
			require.Equal(t, tc.ok, ok)
		})
	}
}
