package env

import "time"

// Core kagent environment variables used by the controller and agent runtime.
var (
	LeaderElect = RegisterBoolVar(
		"LEADER_ELECT",
		true,
		"Enable controller leader election, including during single-replica rolling updates. Set false for local testing.",
		ComponentController,
	)

	MetricsBindAddress = RegisterStringVar(
		"METRICS_BIND_ADDRESS",
		"0",
		"Address the controller-runtime metrics server binds to, e.g. :8080. "+
			"\"0\" (the default) serves no metrics, so an installation that does not "+
			"set this is unchanged. The Helm chart renders this variable, and its "+
			"ServiceMonitor, from controller.metrics.",
		ComponentController,
	)

	MetricsSecure = RegisterBoolVar(
		"METRICS_SECURE",
		false,
		"Serve the metrics endpoint over HTTPS with authentication and authorization. "+
			"A scraper then needs a token bound to the metrics-reader ClusterRole.",
		ComponentController,
	)

	KagentNamespace = RegisterStringVar(
		"KAGENT_NAMESPACE",
		"kagent",
		"Kubernetes namespace where kagent resources are deployed.",
		ComponentController,
	)

	KagentControllerName = RegisterStringVar(
		"KAGENT_CONTROLLER_NAME",
		"kagent-controller",
		"Name of the kagent controller service.",
		ComponentController,
	)

	KagentA2ADebugAddr = RegisterStringVar(
		"KAGENT_A2A_DEBUG_ADDR",
		"",
		"Debug address for the A2A server. When set, all A2A HTTP requests are dialed to this address.",
		ComponentController,
	)

	KagentA2AClientTimeout = RegisterDurationVar(
		"KAGENT_A2A_CLIENT_TIMEOUT",
		0,
		"HTTP client timeout for A2A requests from the controller to agent pods. "+
			"0 (the default) means no timeout, which is recommended for long-running agents "+
			"that stream responses over SSE. Set a positive duration (e.g. 30m) only if you "+
			"need a hard upper bound on individual A2A calls.",
		ComponentController,
	)

	PausedRuntimeTTL = RegisterDurationVar(
		"KAGENT_PAUSED_RUNTIME_TTL",
		2*time.Minute,
		"How long an AgentInstance runtime paused for a person's input (a task in "+
			"input-required or auth-required) stays checkpointed on its worker before the "+
			"controller suspends it to the snapshot store, out of reach of a node loss. "+
			"A reply within the TTL resumes the runtime in place; a later one restores it "+
			"from the snapshot. 0 disables the suspend.",
		ComponentController,
	)

	StalledTurnTimeout = RegisterDurationVar(
		"KAGENT_STALLED_TURN_TIMEOUT",
		time.Hour,
		"How long a working A2A task may go without a recorded event while no run of "+
			"the gateway follows it before the controller fails it as interrupted. Such a "+
			"task's runtime or controller process was lost mid-turn; failing it releases "+
			"the AgentInstance for the next message. 0 disables the sweep.",
		ComponentController,
	)

	ShareMaxTTL = RegisterDurationVar(
		"KAGENT_SHARE_MAX_TTL",
		0,
		"Longest lifetime an AgentInstance share may request. A share created "+
			"without a ttl receives it. 0 leaves shares valid until they are revoked "+
			"or their instance is deleted.",
		ComponentController,
	)

	// Variables injected into agent pods (not read by the controller itself).

	KagentName = RegisterStringVar(
		"KAGENT_NAME",
		"",
		"Name of the agent. Injected into agent pods via the controller.",
		ComponentAgentRuntime,
	)

	KagentAgentTemplate = RegisterStringVar(
		"KAGENT_AGENT_TEMPLATE",
		"",
		"Name of the AgentTemplate the runtime executes. With KAGENT_NAMESPACE it is the identity "+
			"the runtime sends on every model call, as the request headers x-kagent-agent and "+
			"x-kagent-agent-namespace. Injected into agent runtimes via the controller.",
		ComponentAgentRuntime,
	)

	KagentAPIURL = RegisterStringVar(
		"KAGENT_API_URL",
		"",
		"Base URL for kagent control-plane API calls.",
		ComponentAgentRuntime,
	)

	KagentGatewayURL = RegisterStringVar(
		"KAGENT_GATEWAY_URL",
		"",
		"Base URL for A2A and MCP traffic.",
		ComponentAgentRuntime,
	)

	KagentUIURL = RegisterStringVar(
		"KAGENT_UI_URL",
		"",
		"Public base URL of the kagent UI (e.g. https://kagent.example.com). "+
			"When set, share link tools return full clickable URLs instead of paths.",
		ComponentAgentRuntime,
	)

	KagentSkillsFolder = RegisterStringVar(
		"KAGENT_SKILLS_FOLDER",
		"/skills",
		"Directory path where agent skills are mounted.",
		ComponentAgentRuntime,
	)

	KagentPropagateToken = RegisterStringVar(
		"KAGENT_PROPAGATE_TOKEN",
		"",
		"When set, propagates the authentication token to downstream services.",
		ComponentAgentRuntime,
	)

	// Registered here for `kagent env` CLI discoverability only -- the
	// actual gate is read independently (raw os.Getenv, not via this var)
	// in go/adk/pkg/tools/skills.go's enableFileSearchToolsEnv. The two
	// literals are pinned together by that package's
	// TestEnableFileSearchToolsEnvMatchesRegistry.
	KagentEnableFileSearchTools = RegisterBoolVar(
		"KAGENT_ENABLE_FILE_SEARCH_TOOLS",
		false,
		"When true, enables the list_files and grep_file skills tools, which let an agent "+
			"enumerate and search the filesystem under its session/skills roots without a "+
			"shell. Disabled by default; set on the Agent's env to opt in.",
		ComponentAgentRuntime,
	)

	StsWellKnownURI = RegisterStringVar(
		"STS_WELL_KNOWN_URI",
		"",
		"Well-known endpoint for the Security Token Service (STS) used for token exchange.",
		ComponentAgentRuntime,
	)

	KagentSTSResource = RegisterStringVar(
		"KAGENT_STS_RESOURCE",
		"",
		"RFC 8707 resource indicator sent on STS token-exchange requests to scope the issued token to a target backend.",
		ComponentAgentRuntime,
	)

	KagentSTSAudience = RegisterStringVar(
		"KAGENT_STS_AUDIENCE",
		"",
		"RFC 8693 audience sent on STS token-exchange requests. Alternate to KAGENT_STS_RESOURCE for servers that key on audience.",
		ComponentAgentRuntime,
	)

	CallerCredentialsAddress = RegisterStringVar(
		"KAGENT_CALLER_CREDENTIALS_ADDRESS",
		"",
		"Address the caller credential provider serves Substrate's egress gateway on, "+
			"e.g. :8443: the token of the person whose turn runs on an actor, for the "+
			"Harness callerCredentials. Empty serves no provider, and a caller credential "+
			"is then refused by the gateway.",
		ComponentController,
	)

	CallerCredentialsServerCredBundle = RegisterStringVar(
		"KAGENT_CALLER_CREDENTIALS_SERVER_CRED_BUNDLE",
		"",
		"PEM file holding the provider's serving certificate chain and key, read on every "+
			"handshake (a Substrate servicedns pod certificate).",
		ComponentController,
	)

	CallerCredentialsClientCAFile = RegisterStringVar(
		"KAGENT_CALLER_CREDENTIALS_CLIENT_CA_FILE",
		"",
		"CA bundle the egress gateway's client certificate chains to (Substrate's "+
			"podidentity trust bundle), read on every handshake.",
		ComponentController,
	)

	CallerCredentialsInjectorSPIFFEID = RegisterStringVar(
		"KAGENT_CALLER_CREDENTIALS_INJECTOR_SPIFFE_ID",
		"spiffe://cluster.local/ns/ate-system/sa/atenet-egress",
		"URI SAN the egress gateway's client certificate must carry; no other client is answered.",
		ComponentController,
	)

	TokenBrokerURL = RegisterStringVar(
		"KAGENT_TOKEN_BROKER_URL",
		"",
		"RFC 8693 token endpoint the caller credential provider exchanges a turn caller's "+
			"token at, e.g. muster's http://muster.agent-platform.svc:8090/oauth/token.",
		ComponentController,
	)

	TokenBrokerClientID = RegisterStringVar(
		"KAGENT_TOKEN_BROKER_CLIENT_ID",
		"",
		"Client ID the caller credential provider authenticates to the token broker with (HTTP Basic).",
		ComponentController,
	)

	TokenBrokerClientSecretFile = RegisterStringVar(
		"KAGENT_TOKEN_BROKER_CLIENT_SECRET_FILE",
		"",
		"File holding the token broker client secret, read on every exchange.",
		ComponentController,
	)

	TokenBrokerSubjectTokenType = RegisterStringVar(
		"KAGENT_TOKEN_BROKER_SUBJECT_TOKEN_TYPE",
		"urn:ietf:params:oauth:token-type:id_token",
		"RFC 8693 subject_token_type of the caller's token: what the platform's edge forwards as the bearer.",
		ComponentController,
	)

	DatabaseVectorEnabled = RegisterBoolVar(
		"DATABASE_VECTOR_ENABLED",
		false,
		"Enable vector database migrations and vector-backed database functionality.",
		ComponentDatabase,
	)

	SkipMigrations = RegisterBoolVar(
		"SKIP_MIGRATIONS",
		false,
		"Verify required database migrations at startup without applying them.",
		ComponentDatabase,
	)
)
