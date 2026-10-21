package driver

import "github.com/kagent-dev/kagent/go/harness/runtime"

type EventKind string

const (
	EventSessionStarted EventKind = "session_started"
	EventTextDelta      EventKind = "text_delta"
	EventToolActivity   EventKind = "tool_activity"
	EventCompleted      EventKind = "completed"
	EventFailed         EventKind = "failed"
	EventModelCall      EventKind = "model_call"
	EventDeferred       EventKind = "deferred"
	// EventBackgroundTasks lists the commands and agents Claude runs in the
	// background, in full, each time that set changes.
	EventBackgroundTasks EventKind = "background_tasks"
)

// Event is the Claude stream vocabulary consumed by ProcessDriver. Vendor
// parsing details stay here and are normalized before reaching shared runtime
// code.
type Event struct {
	Kind      EventKind
	SessionID string
	Text      string
	ToolID    string
	ToolName  string
	ToolPhase string
	// ParentToolID is the Agent call whose subagent started the tool.
	ParentToolID string
	ToolResult   any
	ToolError    bool
	Metadata     map[string]any
	SafeMessage  string
	Deferred     *DeferredTool
	ModelCall    *runtime.ModelCall
	// BackgroundTasks describes each task of an EventBackgroundTasks.
	BackgroundTasks []string
}
