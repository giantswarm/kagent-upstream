package a2a

const (
	// RuntimeLostMessagePrefix opens the status message of a task the gateway
	// failed because the AgentInstance's runtime is gone for good — its Actor
	// crashed or no longer exists — and the message of the failure the
	// instance records for it. The text after the prefix names the cause. A
	// client that reads it knows the conversation cannot continue on this
	// instance, as opposed to a turn that failed and may be retried.
	RuntimeLostMessagePrefix = "runtime lost: "

	// FailureReasonRuntimeLost is the AgentInstance failure reason recorded
	// with a message that opens with RuntimeLostMessagePrefix.
	FailureReasonRuntimeLost = "RuntimeLost"
)
