package a2a

import (
	"slices"
	"strings"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func extensionRequested(callContext *a2asrv.CallContext, uri string) bool {
	if callContext == nil {
		return false
	}
	for _, value := range callContext.Extensions().RequestedURIs() {
		for requested := range strings.SplitSeq(value, ",") {
			if strings.TrimSpace(requested) == uri {
				return true
			}
		}
	}
	return false
}

func ensureExtension(card *a2atype.AgentCard, extension a2atype.AgentExtension) {
	if card == nil || slices.ContainsFunc(card.Capabilities.Extensions, func(declared a2atype.AgentExtension) bool {
		return declared.URI == extension.URI
	}) {
		return
	}
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, extension)
}
