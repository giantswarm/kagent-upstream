package a2a

import (
	"math"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
)

// usageCostField is the field of the usage metadata that carries what a turn
// cost in US dollars, as its runtime reported it.
const usageCostField = "costUsd"

// TurnCostUSDOf reads the cost a status message's usage reports for its turn.
// reported is false when the message carries no cost, as a runtime that
// reports none (Codex) leaves it; ok is false when the usage or its cost is
// malformed: not an object, or a cost that is not a finite, non-negative
// number.
func TurnCostUSDOf(carrier a2atype.MetadataCarrier) (cost float64, reported, ok bool) {
	if carrier == nil {
		return 0, false, true
	}
	value, present := carrier.Meta()[UsageMetadataKey]
	if !present {
		return 0, false, true
	}
	fields, isObject := value.(map[string]any)
	if !isObject {
		return 0, false, false
	}
	raw, present := fields[usageCostField]
	if !present {
		return 0, false, true
	}
	cost, isNumber := raw.(float64)
	if !isNumber || cost < 0 || math.IsInf(cost, 0) || math.IsNaN(cost) {
		return 0, false, false
	}
	return cost, true, true
}
