# Codex runs each command in a login shell, whose /etc/profile resets PATH:
# the tool image bin directories (KAGENT_TOOL_PATHS) follow the image's again.
if [ -n "${KAGENT_TOOL_PATHS:-}" ]; then
	PATH="${PATH}:${KAGENT_TOOL_PATHS}"
	export PATH
fi
