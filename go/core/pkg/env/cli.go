package env

// CLI-specific environment variables used by the kagent CLI tool.
var (
	KagentDefaultModelProvider = RegisterStringVar(
		"KAGENT_DEFAULT_MODEL_PROVIDER",
		"openAI",
		"Default LLM provider for agents (e.g. openAI, anthropic, ollama, azureOpenAI).",
		ComponentCLI,
	)

	KagentHelmRepo = RegisterStringVar(
		"KAGENT_HELM_REPO",
		"oci://ghcr.io/kagent-dev/kagent/helm/",
		"Helm repository URL for kagent charts.",
		ComponentCLI,
	)

	KagentHelmVersion = RegisterStringVar(
		"KAGENT_HELM_VERSION",
		"",
		"Helm chart version to deploy.",
		ComponentCLI,
	)

	KagentHelmExtraArgs = RegisterStringVar(
		"KAGENT_HELM_EXTRA_ARGS",
		"",
		"Additional arguments to pass to Helm commands.",
		ComponentCLI,
	)

	KagentToken = RegisterStringVar(
		"KAGENT_TOKEN",
		"",
		"Caller token the CLI sends as the bearer of every controller call, by which a controller in trusted-proxy mode identifies the caller. --caller-token overrides it; when both are unset the CLI sends the credential of the current kubeconfig context (an exec plugin, an id-token or a token).",
		ComponentCLI,
	)
)
