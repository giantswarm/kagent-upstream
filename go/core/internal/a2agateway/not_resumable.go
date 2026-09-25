package a2agateway

import (
	"errors"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/errordetails"
)

// notResumableCause names the loss of a runtime Substrate refused to resume
// for good, in the words the person reading the failed turn needs: the Actor's
// template no longer has the snapshot its state restores onto, as after an
// upgrade of the workers the conversation began on.
const notResumableCause = "this conversation was started before its agent's runtime was upgraded and cannot be continued; start a new conversation"

// substrateResumableKey is the ErrorInfo metadata key by which Substrate marks
// a resume refusal no retry outlives (value "false"). The A2A client keeps an
// ErrorInfo's metadata where it drops a reason A2A does not define.
const substrateResumableKey = "resumable"

// notResumable reports whether a runtime stream failed on a resume refusal
// Substrate marked not resumable.
func notResumable(err error) bool {
	var a2aErr *a2atype.Error
	if !errors.As(err, &a2aErr) {
		return false
	}
	for _, detail := range a2aErr.TypedDetails {
		if detail.TypeURL != errordetails.ErrorInfoType {
			continue
		}
		if metadata, ok := detail.Value["metadata"].(map[string]string); ok && metadata[substrateResumableKey] == "false" {
			return true
		}
	}
	return false
}
