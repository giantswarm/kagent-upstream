package driver

import "github.com/kagent-dev/kagent/go/harness/runtime"

type EventKind string

const (
	EventSessionStarted EventKind = "session_started"
	EventTextDelta      EventKind = "text_delta"
	EventToolActivity   EventKind = "tool_activity"
	EventCompleted      EventKind = "completed"
	EventFailed         EventKind = "failed"
	// EventBackgroundTasks lists the commands and agents Claude runs in the
	// background, in full, each time that set changes.
	EventBackgroundTasks EventKind = "background_tasks"
	// EventControlRequest is a request Claude sends its host on the
	// stream-JSON control channel and waits for an answer to.
	EventControlRequest EventKind = "control_request"
)

// ControlRequest is one stream-JSON control request. Only MCP elicitations
// carry the fields after Subtype.
type ControlRequest struct {
	RequestID     string
	Subtype       string
	Server        string
	Message       string
	Mode          string
	URL           string
	ElicitationID string
}

// Event is the Claude stream vocabulary consumed by ProcessDriver. Vendor
// parsing details stay here and are normalized before reaching shared runtime
// code.
type Event struct {
	Kind        EventKind
	SessionID   string
	Text        string
	ToolID      string
	ToolName    string
	ToolPhase   string
	ToolResult  any
	ToolError   bool
	Metadata    map[string]any
	Category    string
	SafeMessage string
	Result      string
	Usage       *runtime.Usage
	// BackgroundTasks describes each task of an EventBackgroundTasks.
	BackgroundTasks []string
	// Control is the request of an EventControlRequest.
	Control *ControlRequest
}
