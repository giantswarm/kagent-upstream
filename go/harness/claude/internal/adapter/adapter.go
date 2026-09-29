// Package adapter constructs the Claude runtime from compiler-owned
// configuration and Actor-owned paths.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kagent-dev/kagent/go/core/pkg/agentplugins"
	"github.com/kagent-dev/kagent/go/harness/claude/config"
	"github.com/kagent-dev/kagent/go/harness/claude/internal/driver"
	"github.com/kagent-dev/kagent/go/harness/internal/utils"
	"github.com/kagent-dev/kagent/go/pkg/telemetry"
	"github.com/kagent-dev/kagent/go/pkg/tracing"
)

const (
	approvalMCPServerName = "kagent_hitl"
	// projectSettingSource loads the workspace's project instructions
	// (CLAUDE.md, AGENTS.md) and project settings.
	projectSettingSource = "project"
)

var callerRouteHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// Input contains compiler output and Actor-owned locations used to construct
// the Claude driver.
type Input struct {
	ConfigJSON   []byte
	Workspace    string
	DurableDir   string
	EphemeralDir string
	Environment  []string
}

// New validates and materializes Claude-owned state, then constructs its driver.
func New(ctx context.Context, input Input) (*driver.ProcessDriver, error) {
	cfg, err := config.Parse(input.ConfigJSON)
	if err != nil {
		return nil, err
	}
	agentsJSON, err := cfg.AgentsJSON()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(input.Workspace) || !filepath.IsAbs(input.DurableDir) || !filepath.IsAbs(input.EphemeralDir) {
		return nil, fmt.Errorf("workspace, durable, and ephemeral directories must be absolute paths")
	}
	claudeDir := filepath.Join(input.DurableDir, "claude")
	var skillRoot string
	if cfg.SkillResources != nil {
		skillRoot = filepath.Join(input.DurableDir, "generated", "claude")
	}
	runsAsRoot := os.Geteuid() == 0
	claudeTrees := []string{input.Workspace, claudeDir, input.EphemeralDir}
	if skillRoot != "" {
		claudeTrees = append(claudeTrees, skillRoot)
	}
	if runsAsRoot {
		// Lab stand-in for agent-substrate/substrate#1918: gVisor gives the
		// container's root the 0700 mode of the bundle's upper directory.
		if err := os.Chmod("/", 0o755); err != nil {
			return nil, fmt.Errorf("make the root searchable: %w", err)
		}
		for _, tree := range claudeTrees {
			if err := utils.ReclaimTree(tree); err != nil {
				return nil, fmt.Errorf("reclaim %s from the unprivileged user: %w", tree, err)
			}
		}
	}
	for _, directory := range []struct{ name, path string }{
		{name: "workspace", path: input.Workspace},
		{name: "Claude state", path: claudeDir},
	} {
		if err := utils.EnsurePrivateDir(directory.path); err != nil {
			return nil, fmt.Errorf("prepare %s directory: %w", directory.name, err)
		}
	}
	var pluginDirs []string
	if cfg.SkillResources != nil {
		skillsDir := filepath.Join(skillRoot, ".claude", "skills")
		if err := utils.EnsurePrivateDir(skillsDir); err != nil {
			return nil, fmt.Errorf("prepare generated Claude skills directory: %w", err)
		}
		materialized, err := agentplugins.Materialize(ctx, *cfg.SkillResources, agentplugins.Paths{
			Packages: filepath.Join(claudeDir, "packages"),
			Skills:   skillsDir,
		})
		if err != nil {
			return nil, fmt.Errorf("materialize Claude skills: %w", err)
		}
		pluginDirs = materialized.ClaudeFormatPluginRoots()
	}
	environment := setEnvironment(input.Environment, config.ClaudeConfigDirEnvName, claudeDir)
	// The native runtime inherits the compiled identity through the standard
	// resource variable, so no user-supplied marker is required.
	environment = nativeTelemetryEnvironment(telemetry.WithDefaults(tracing.ResourceEnvironment(environment, cfg.RuntimeTelemetry.ChildResource())), cfg.RuntimeTelemetry)
	// The image and compiler pin an exact Claude version. Prevent both automatic
	// and manual update paths from changing that runtime after validation.
	environment = setEnvironment(environment, config.DisableUpdatesEnvName, "1")
	environment, err = materializeGoogleCredentials(environment, input.EphemeralDir)
	if err != nil {
		return nil, err
	}
	if _, exists := cfg.MCPServers[approvalMCPServerName]; exists {
		return nil, fmt.Errorf("Claude MCP server name %q is reserved for human approval", approvalMCPServerName)
	}
	var forwarder *driver.CredentialForwarder
	var approvalBroker *driver.ApprovalBroker
	closeListeners := func() {
		if forwarder != nil {
			_ = forwarder.Close()
		}
		if approvalBroker != nil {
			_ = approvalBroker.Close()
		}
	}
	routes, err := callerRoutes(input.Environment)
	if err != nil {
		return nil, err
	}
	propagate := propagateCallerToken(input.Environment)
	if len(routes) != 0 && !propagate {
		return nil, fmt.Errorf("%s requires %s=true: a caller route acts as the turn's caller", config.CallerRoutesEnvName, config.PropagateTokenEnvName)
	}
	if propagate && (len(cfg.MCPServers) != 0 || len(routes) != 0) {
		forwarder, err = frontMCPServers(&cfg, routes)
		if err != nil {
			return nil, err
		}
		environment, err = gitRouteEnvironment(environment, forwarder, slices.Sorted(maps.Keys(routes)))
		if err != nil {
			closeListeners()
			return nil, err
		}
	}
	protectedServers := approvalServerNames(cfg.MCPServers)
	if err := utils.EnsurePrivateDir(input.EphemeralDir); err != nil {
		closeListeners()
		return nil, fmt.Errorf("prepare ephemeral Claude settings directory: %w", err)
	}
	settings := map[string]any{}
	var settingSources string
	if projectInstructions(input.Environment) {
		settingSources = projectSettingSource
		settings["disableAllHooks"] = true
	}
	var permissionPromptTool string
	if len(protectedServers) != 0 {
		approvalBroker, err = driver.NewApprovalBroker(protectedServers, cfg.MaxEventBytes)
		if err != nil {
			closeListeners()
			return nil, fmt.Errorf("start Claude approval broker: %w", err)
		}
		settings["permissions"] = approvalBroker.Permissions()
		permissionPromptTool = "mcp__" + approvalMCPServerName + "__" + driver.ApprovalToolName
		mcpServers := make(map[string]config.MCPServer, len(cfg.MCPServers)+1)
		maps.Copy(mcpServers, cfg.MCPServers)
		mcpServers[approvalMCPServerName] = config.MCPServer{
			Type: "http", URL: approvalBroker.URL(), Headers: approvalBroker.Headers(),
		}
		cfg.MCPServers = mcpServers
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		closeListeners()
		return nil, fmt.Errorf("encode Claude settings: %w", err)
	}
	settingsPath := filepath.Join(input.EphemeralDir, "settings.json")
	if err := utils.ReplacePrivateFile(settingsPath, settingsJSON); err != nil {
		closeListeners()
		return nil, fmt.Errorf("materialize Claude settings: %w", err)
	}
	mcpJSON, err := cfg.MCPConfigJSON()
	if err != nil {
		closeListeners()
		return nil, err
	}
	var mcpConfigPath string
	if len(mcpJSON) != 0 {
		if err := utils.EnsurePrivateDir(input.EphemeralDir); err != nil {
			closeListeners()
			return nil, fmt.Errorf("prepare ephemeral MCP directory: %w", err)
		}
		mcpConfigPath = filepath.Join(input.EphemeralDir, "mcp.json")
		if err := utils.ReplacePrivateFile(mcpConfigPath, mcpJSON); err != nil {
			closeListeners()
			return nil, fmt.Errorf("materialize Claude MCP configuration: %w", err)
		}
	}
	var runAs *driver.Identity
	if runsAsRoot {
		if err := handOver(input.DurableDir, claudeTrees); err != nil {
			closeListeners()
			return nil, err
		}
		runAs = &driver.Identity{UID: config.UnprivilegedUID, GID: config.UnprivilegedGID}
		environment = setEnvironment(environment, config.HomeEnvName, claudeDir)
	}
	processConfig := driver.ProcessConfig{
		Executable: cfg.ClaudeExecutable, ExpectedVersion: cfg.ExpectedClaudeVersion,
		StrictVersion: cfg.StrictVersion, Workspace: input.Workspace, Model: cfg.Model,
		AppendSystemPrompt: cfg.AppendSystemPrompt, AgentsJSON: agentsJSON, MCPConfigPath: mcpConfigPath,
		SettingsPath: settingsPath, SettingSources: settingSources, PermissionPromptTool: permissionPromptTool, ApprovalBroker: approvalBroker,
		SkillRoot: skillRoot, PluginDirs: pluginDirs, Environment: environment,
		MaxEventBytes: cfg.MaxEventBytes, MaxStderrBytes: cfg.MaxStderrBytes,
		InterruptGrace: cfg.InterruptGrace(), MaxBudgetUSD: cfg.MaxBudgetUSD, MaxTurns: cfg.MaxTurns, RunAs: runAs,
	}
	if forwarder != nil {
		processConfig.CallerCredentials = forwarder
	}
	return driver.NewProcessDriver(processConfig), nil
}

// propagateCallerToken reports whether the Actor environment asks for the
// caller's credential on MCP calls, the same switch the Go ADK reads.
// projectInstructions reports whether the Harness lets Claude read the
// workspace's CLAUDE.md and AGENTS.md; off, a cloned repository adds nothing to
// a turn.
func projectInstructions(environment []string) bool {
	return strings.EqualFold(strings.TrimSpace(environmentValue(environment, config.ProjectInstructionsEnvName)), "true")
}

func propagateCallerToken(environment []string) bool {
	return strings.EqualFold(strings.TrimSpace(environmentValue(environment, config.PropagateTokenEnvName)), "true")
}

// callerRoutes reads the caller routes of the Harness environment.
func callerRoutes(environment []string) (map[string]driver.CallerRoute, error) {
	raw := strings.TrimSpace(environmentValue(environment, config.CallerRoutesEnvName))
	if raw == "" {
		return nil, nil
	}
	var urls map[string]string
	if err := json.Unmarshal([]byte(raw), &urls); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object from host to URL: %w", config.CallerRoutesEnvName, err)
	}
	routes := make(map[string]driver.CallerRoute, len(urls))
	for host, url := range urls {
		if !callerRouteHostPattern.MatchString(host) {
			return nil, fmt.Errorf("%s host %q must be a lowercase host name with an optional port", config.CallerRoutesEnvName, host)
		}
		routes[host] = driver.CallerRoute{URL: url}
	}
	return routes, nil
}

// gitRouteEnvironment points git at the caller routes through git's
// environment configuration: https://<host>/ is rewritten to the host's
// loopback route, and git authenticates to the forwarder with its loopback
// token. That token already reaches Claude through mcp.json; it acts only
// while a turn has bound its caller's credential.
func gitRouteEnvironment(environment []string, forwarder *driver.CredentialForwarder, hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return environment, nil
	}
	if environmentValue(environment, "GIT_CONFIG_COUNT") != "" {
		return nil, fmt.Errorf("%s configures git through GIT_CONFIG_COUNT, which the Harness environment already sets", config.CallerRoutesEnvName)
	}
	entries := [][2]string{{"http." + forwarder.BaseURL() + ".extraHeader", "Authorization: " + forwarder.Headers()["Authorization"]}}
	for _, host := range hosts {
		entries = append(entries, [2]string{"url." + forwarder.RouteURL(host) + ".insteadOf", "https://" + host + "/"})
	}
	environment = setEnvironment(environment, "GIT_CONFIG_COUNT", strconv.Itoa(len(entries)))
	for i, entry := range entries {
		environment = setEnvironment(environment, "GIT_CONFIG_KEY_"+strconv.Itoa(i), entry[0])
		environment = setEnvironment(environment, "GIT_CONFIG_VALUE_"+strconv.Itoa(i), entry[1])
	}
	return environment, nil
}

// frontMCPServers starts the credential forwarder for the compiled MCP servers
// and the caller routes and rewrites the servers to their loopback endpoints,
// so the written configuration carries no upstream URL, no static header and
// no credential.
func frontMCPServers(cfg *config.Config, routes map[string]driver.CallerRoute) (*driver.CredentialForwarder, error) {
	upstream := make(map[string]driver.UpstreamMCPServer, len(cfg.MCPServers))
	for name, server := range cfg.MCPServers {
		if server.Type != "http" {
			return nil, fmt.Errorf("caller credential forwarding requires streamable HTTP MCP servers; %q uses %q", name, server.Type)
		}
		upstream[name] = driver.UpstreamMCPServer{URL: server.URL, Headers: server.Headers}
	}
	forwarder, err := driver.NewCredentialForwarder(upstream, routes, cfg.MaxEventBytes)
	if err != nil {
		return nil, fmt.Errorf("start Claude credential forwarder: %w", err)
	}
	fronted := make(map[string]config.MCPServer, len(cfg.MCPServers))
	for name, server := range cfg.MCPServers {
		fronted[name] = config.MCPServer{
			Type: "http", URL: forwarder.URL(name), Headers: forwarder.Headers(),
			RequireApproval: server.RequireApproval,
		}
	}
	cfg.MCPServers = fronted
	return forwarder, nil
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

func approvalServerNames(servers map[string]config.MCPServer) (protected []string) {
	for name, server := range servers {
		if server.RequireApproval {
			protected = append(protected, name)
		}
	}
	return protected
}

func materializeGoogleCredentials(environment []string, directory string) ([]string, error) {
	// The compiler injects the Secret value as JSON, while Google ADC expects a
	// file path. Keep the credential in ephemeral Actor storage rather than the
	// well-known path under /data, which is durable and may be snapshotted.
	prefix := config.GoogleCredentialsJSONEnvName + "="
	var credentials string
	filtered := make([]string, 0, len(environment))
	for _, item := range environment {
		if strings.HasPrefix(item, prefix) {
			if credentials != "" {
				return nil, fmt.Errorf("%s is configured more than once", config.GoogleCredentialsJSONEnvName)
			}
			credentials = strings.TrimPrefix(item, prefix)
			continue
		}
		filtered = append(filtered, item)
	}
	if credentials == "" {
		return filtered, nil
	}
	if !json.Valid([]byte(credentials)) {
		return nil, fmt.Errorf("%s must contain valid JSON", config.GoogleCredentialsJSONEnvName)
	}
	if err := utils.EnsurePrivateDir(directory); err != nil {
		return nil, fmt.Errorf("prepare ephemeral credentials directory: %w", err)
	}
	path := filepath.Join(directory, "google-credentials.json")
	if err := utils.ReplacePrivateFile(path, []byte(credentials)); err != nil {
		return nil, fmt.Errorf("materialize Google credentials: %w", err)
	}
	return setEnvironment(filtered, config.GoogleApplicationCredentialsEnvName, path), nil
}

// handOver gives Claude's trees to the image's unprivileged user, which Claude
// Code runs as when the harness runs as root, so the harness process and the
// turn's credential in its memory are out of reach of Claude and its tools.
// The directories above a tree, up to the durable directory, stay the
// harness's and only gain search permission: Claude can reach its trees but
// cannot replace the harness's own state beside them.
func handOver(durableDir string, trees []string) error {
	for _, tree := range trees {
		if err := utils.ChownTree(tree, config.UnprivilegedUID, config.UnprivilegedGID); err != nil {
			return fmt.Errorf("hand %s to the unprivileged user: %w", tree, err)
		}
		for dir := filepath.Dir(tree); strings.HasPrefix(dir, durableDir); dir = filepath.Dir(dir) {
			if err := searchableByAll(dir); err != nil {
				return fmt.Errorf("let the unprivileged user traverse %s: %w", dir, err)
			}
			if dir == durableDir {
				break
			}
		}
	}
	return nil
}

func searchableByAll(dir string) error {
	if err := os.Lchown(dir, 0, 0); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return os.Chmod(dir, info.Mode().Perm()|0o111)
}

// nativeTelemetryEnvironment turns on Claude Code telemetry for the signals the
// controller exports, and its content flags when capture is on.
func nativeTelemetryEnvironment(environment []string, telemetry tracing.RuntimeTelemetry) []string {
	exported := func(name string) bool {
		return slices.Contains(environment, name+"=otlp")
	}
	traces, logs := exported("OTEL_TRACES_EXPORTER"), exported("OTEL_LOGS_EXPORTER")
	if !traces && !logs && !exported("OTEL_METRICS_EXPORTER") {
		return environment
	}
	flags := []string{"CLAUDE_CODE_ENABLE_TELEMETRY", "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"}
	if telemetry.CaptureContent {
		flags = append(flags, "OTEL_LOG_USER_PROMPTS", "OTEL_LOG_TOOL_DETAILS")
		if traces {
			flags = append(flags, "OTEL_LOG_TOOL_CONTENT")
		}
		if logs {
			flags = append(flags, "OTEL_LOG_ASSISTANT_RESPONSES")
		}
	}
	for _, flag := range flags {
		environment = setEnvironment(environment, flag, "1")
	}
	return environment
}

func setEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
