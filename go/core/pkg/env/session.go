package env

import "time"

var SessionIdleTTL = RegisterDurationVar("KAGENT_SESSION_IDLE_TTL", 7*24*time.Hour, "Delete sessions after this idle duration. Zero disables expiration; running and waiting tasks are retained.", ComponentController)

var SessionExpirationPollInterval = RegisterDurationVar("KAGENT_SESSION_EXPIRATION_POLL_INTERVAL", time.Minute, "Interval between idle session expiration sweeps. Must be positive.", ComponentController)

var SessionShareMaxTTL = RegisterDurationVar("KAGENT_SESSION_SHARE_MAX_TTL", 0, "Longest lifetime a session share may request. Shares created without a ttl receive it. Zero leaves shares unbounded.", ComponentController)
var SessionRevisionRepointInterval = RegisterDurationVar("KAGENT_REVISION_REPOINT_INTERVAL", 5*time.Minute, "How often the controller moves the quiesced runtimes of sessions whose revision their agent has superseded onto the agent's current revision, so a conversation nobody writes to again does not keep the old revision and its ActorTemplate alive. A turn moves its own runtime regardless. Zero disables the sweep.", ComponentController)
