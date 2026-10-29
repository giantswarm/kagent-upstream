// Package config reads the ADK-shaped configuration Kagent compiles for a BYO
// Harness and narrows it to what the omp adapter implements. A field the
// adapter cannot honour is refused with the reason rather than dropped, so an
// Agent never runs with less than its definition asks for.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/api/agentplugin"
)

const (
	// PinnedOmpVersion is the omp release the image installs; the adapter
	// refuses any other binary, so a protocol change arrives only with a
	// reviewed bump. Renovate moves it with the Dockerfile's OMP_VERSION.
	PinnedOmpVersion = "18.14.0"
)

// Config is the part of the compiled Agent the omp adapter runs.
type Config struct {
	Model       Model
	Instruction string
	// MCPServers are the Agent's Streamable HTTP servers in compiled order,
	// each under a stable name omp shows the model as the tool prefix.
	MCPServers []MCPServer
	// Skills are the root's selections and every Shared agent's, in one
	// selection: omp loads all of them from one directory.
	Skills *agentplugin.Resources
	// SharedAgents are the Agent's Shared subagent bindings, which omp runs
	// through its task tool in the root's process.
	SharedAgents []SharedAgent
}

// SharedAgent is a Shared subagent binding as an omp task agent. It runs on
// the root's provider, so only its model ID is its own.
type SharedAgent struct {
	Name        string
	Description string
	Instruction string
	ModelID     string
	// Skills are the names of the skills it selects, all of them in
	// Config.Skills.
	Skills []string
}

// ModelIDs are the root's model and every other model a Shared agent runs,
// each once, the root's first.
func (c Config) ModelIDs() []string {
	ids := []string{c.Model.ID}
	for _, agent := range c.SharedAgents {
		if !slices.Contains(ids, agent.ModelID) {
			ids = append(ids, agent.ModelID)
		}
	}
	return ids
}

// Model is an Anthropic Messages endpoint. The API key never appears here: omp
// reads it from the process environment, where the controller has put the
// placeholder the egress gateway replaces with the Secret.
type Model struct {
	ID        string
	BaseURL   string
	Headers   map[string]string
	MaxTokens int
}

// MCPServer is one Streamable HTTP server. Header values are final: a
// Secret-backed header already holds the placeholder the egress gateway
// replaces on the server's origin.
type MCPServer struct {
	Name    string
	URL     string
	Headers map[string]string
	// HeaderVariables are the environment variables the headers resolved
	// from; the bridge holds their values, so omp's environment need not.
	HeaderVariables []string
	Tools           []string
	TimeoutSeconds  float64
}

// DefaultAnthropicBaseURL is the endpoint of a ModelConfig without baseUrl.
const DefaultAnthropicBaseURL = "https://api.anthropic.com"

const envTokenPrefix, envTokenSuffix = "__KAGENT_ENV[", "]__"

var serverNameInvalid = regexp.MustCompile(`[^a-z0-9_]+`)

// sharedAgentName is a binding name omp's agent directory can hold as
// <name>.md and its task tool can address.
var sharedAgentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// reservedAgentNames are the names omp gives its own sessions and bundled task
// agents. omp ignores an agent file named main or sub, and a user agent of a
// bundled agent's name replaces it, the default task agent included.
var reservedAgentNames = []string{"main", "sub", "task", "sonic", "scout", "reviewer", "security-reviewer", "seance"}

// Parse decodes KAGENT_CONFIG_JSON. lookupEnv resolves the __KAGENT_ENV[NAME]__
// tokens the compiler writes for Secret-backed headers.
func Parse(raw []byte, lookupEnv func(string) (string, bool)) (Config, error) {
	var agent adk.AgentConfig
	if err := json.Unmarshal(raw, &agent); err != nil {
		return Config{}, fmt.Errorf("decode agent configuration: %w", err)
	}
	if err := refuseUnsupported(&agent); err != nil {
		return Config{}, err
	}
	model, err := parseModel(agent.Model, lookupEnv)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Model: model, Instruction: agent.Instruction}
	skills := &skillSet{}
	if _, err := skills.add(agent.AgentPlugins); err != nil {
		return Config{}, err
	}
	for _, child := range agent.SubAgents {
		shared, err := parseSharedAgent(model, child, skills, lookupEnv)
		if err != nil {
			return Config{}, err
		}
		cfg.SharedAgents = append(cfg.SharedAgents, shared)
	}
	if !skills.empty() {
		cfg.Skills = &skills.resources
	}
	used := map[string]int{}
	for index, tool := range agent.HttpTools {
		server, err := parseMCPServer(index, tool, lookupEnv)
		if err != nil {
			return Config{}, err
		}
		server.Name = uniqueServerName(server.URL, used)
		cfg.MCPServers = append(cfg.MCPServers, server)
	}
	return cfg, nil
}

func refuseUnsupported(agent *adk.AgentConfig) error {
	switch {
	case len(agent.SseTools) > 0:
		return fmt.Errorf("SSE MCP servers are not supported by the omp Harness; use Streamable HTTP")
	case len(agent.StdioTools) > 0:
		return fmt.Errorf("stdio MCP servers are not supported by the omp Harness")
	case len(agent.RemoteAgents) > 0:
		return fmt.Errorf("remote agent bindings are not supported by the omp Harness")
	case agent.Memory != nil:
		return fmt.Errorf("memory is not supported by the omp Harness")
	case agent.Output != nil:
		return fmt.Errorf("structured output is not supported by the omp Harness")
	}
	return nil
}

// parseSharedAgent narrows a Shared binding to what omp's task agents carry: a
// prompt, a model of the root's provider and skills.
func parseSharedAgent(root Model, child *adk.AgentConfig, skills *skillSet, lookupEnv func(string) (string, bool)) (SharedAgent, error) {
	if child == nil {
		return SharedAgent{}, fmt.Errorf("a Shared agent binding has no configuration")
	}
	name := child.Name
	switch {
	case !sharedAgentName.MatchString(name):
		return SharedAgent{}, fmt.Errorf("shared agent %q: the omp Harness needs a binding name of letters, digits, '.', '_' and '-' that starts with a letter or digit", name)
	case slices.Contains(reservedAgentNames, strings.ToLower(name)):
		return SharedAgent{}, fmt.Errorf("shared agent %q: the name is one omp reserves for its own sessions or bundled agents", name)
	case strings.TrimSpace(child.Description) == "":
		return SharedAgent{}, fmt.Errorf("shared agent %q has no description", name)
	case len(child.HttpTools) > 0 || len(child.SseTools) > 0 || len(child.StdioTools) > 0:
		// omp hands a task agent proxies of every MCP tool its parent has;
		// the tools allowlist of an agent file selects built-in tools only.
		return SharedAgent{}, fmt.Errorf("shared agent %q: MCP tools of a Shared agent are not supported by the omp Harness, whose subagents see every MCP tool of the root", name)
	case len(child.SubAgents) > 0 || len(child.RemoteAgents) > 0:
		return SharedAgent{}, fmt.Errorf("shared agent %q: nested agent bindings are not supported by the omp Harness", name)
	case child.Memory != nil:
		return SharedAgent{}, fmt.Errorf("shared agent %q: memory is not supported by the omp Harness", name)
	case child.Output != nil:
		return SharedAgent{}, fmt.Errorf("shared agent %q: structured output is not supported by the omp Harness", name)
	}
	model, err := parseModel(child.Model, lookupEnv)
	if err != nil {
		return SharedAgent{}, fmt.Errorf("shared agent %q: %w", name, err)
	}
	if model.BaseURL != root.BaseURL || model.MaxTokens != root.MaxTokens || !maps.Equal(model.Headers, root.Headers) {
		return SharedAgent{}, fmt.Errorf("shared agent %q must use the root agent's provider and authentication configuration", name)
	}
	selected, err := skills.add(child.AgentPlugins)
	if err != nil {
		return SharedAgent{}, fmt.Errorf("shared agent %q: %w", name, err)
	}
	return SharedAgent{
		Name: name, Description: child.Description, Instruction: child.Instruction, ModelID: model.ID, Skills: selected,
	}, nil
}

// skillSet joins the Agent Plugins selections of the root and its Shared
// agents. A skill selected twice from the same source is installed once; the
// same name from two sources is refused, as one directory holds every skill.
type skillSet struct {
	resources agentplugin.Resources
	origins   map[string]skillOrigin
}

type skillOrigin struct {
	plugin bool
	source agentplugin.Source
}

func (s *skillSet) empty() bool {
	return len(s.resources.Skills) == 0 && len(s.resources.Plugins) == 0
}

// add joins one agent's selections and returns the names it selects.
func (s *skillSet) add(resources *agentplugin.Resources) ([]string, error) {
	if resources == nil {
		return nil, nil
	}
	var names []string
	selects := func(name string, origin skillOrigin) (bool, error) {
		if slices.Contains(names, name) {
			return false, fmt.Errorf("duplicate skill name %q", name)
		}
		names = append(names, name)
		return s.claim(name, origin)
	}
	for _, skill := range resources.Skills {
		added, err := selects(skill.Name, skillOrigin{source: skill.Source})
		if err != nil {
			return nil, err
		}
		if added {
			s.resources.Skills = append(s.resources.Skills, skill)
		}
	}
	for _, bundle := range resources.Plugins {
		index := slices.IndexFunc(s.resources.Plugins, func(existing agentplugin.Bundle) bool {
			return reflect.DeepEqual(existing.Source, bundle.Source)
		})
		if index < 0 {
			s.resources.Plugins = append(s.resources.Plugins, agentplugin.Bundle{Source: bundle.Source})
			index = len(s.resources.Plugins) - 1
		}
		for _, name := range bundle.Skills {
			added, err := selects(name, skillOrigin{plugin: true, source: bundle.Source})
			if err != nil {
				return nil, err
			}
			if added {
				s.resources.Plugins[index].Skills = append(s.resources.Plugins[index].Skills, name)
			}
		}
	}
	return names, nil
}

func (s *skillSet) claim(name string, origin skillOrigin) (bool, error) {
	existing, ok := s.origins[name]
	if !ok {
		if s.origins == nil {
			s.origins = map[string]skillOrigin{}
		}
		s.origins[name] = origin
		return true, nil
	}
	if !reflect.DeepEqual(existing, origin) {
		return false, fmt.Errorf("skill %q is selected from two sources; the omp Harness installs every skill of the Agent and its Shared agents in one directory", name)
	}
	return false, nil
}

func parseModel(model adk.Model, lookupEnv func(string) (string, bool)) (Model, error) {
	anthropic, ok := model.(*adk.Anthropic)
	if !ok || anthropic == nil {
		kind := "none"
		if model != nil {
			kind = model.GetType()
		}
		return Model{}, fmt.Errorf("the omp Harness needs an Anthropic ModelConfig, got %q", kind)
	}
	switch {
	case strings.TrimSpace(anthropic.Model) == "":
		return Model{}, fmt.Errorf("the Anthropic ModelConfig names no model")
	case anthropic.APIKeyPassthrough:
		return Model{}, fmt.Errorf("API key passthrough is not supported by the omp Harness")
	case anthropic.TLSInsecureSkipVerify != nil && *anthropic.TLSInsecureSkipVerify,
		anthropic.TLSCACertPath != nil && *anthropic.TLSCACertPath != "",
		anthropic.TLSDisableSystemCAs != nil && *anthropic.TLSDisableSystemCAs:
		return Model{}, fmt.Errorf("custom model TLS is not supported by the omp Harness")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(anthropic.BaseUrl), "/")
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	if err := requireHTTPURL("model base URL", baseURL); err != nil {
		return Model{}, err
	}
	headers, _, err := expandHeaders(anthropic.Headers, lookupEnv)
	if err != nil {
		return Model{}, fmt.Errorf("model headers: %w", err)
	}
	result := Model{ID: anthropic.Model, BaseURL: baseURL, Headers: headers}
	if anthropic.MaxTokens != nil {
		result.MaxTokens = *anthropic.MaxTokens
	}
	return result, nil
}

func parseMCPServer(index int, tool adk.HttpMcpServerConfig, lookupEnv func(string) (string, bool)) (MCPServer, error) {
	params := tool.Params
	switch {
	case tool.RequireApproval:
		return MCPServer{}, fmt.Errorf("MCP server %d: tool approval is not supported by the omp Harness", index)
	case params.TLSInsecureSkipVerify != nil && *params.TLSInsecureSkipVerify,
		params.TLSCACertPath != nil && *params.TLSCACertPath != "",
		params.TLSDisableSystemCAs != nil && *params.TLSDisableSystemCAs:
		return MCPServer{}, fmt.Errorf("MCP server %d: custom TLS is not supported by the omp Harness", index)
	}
	if err := requireHTTPURL(fmt.Sprintf("MCP server %d URL", index), params.Url); err != nil {
		return MCPServer{}, err
	}
	headers, variables, err := expandHeaders(params.Headers, lookupEnv)
	if err != nil {
		return MCPServer{}, fmt.Errorf("MCP server %d headers: %w", index, err)
	}
	server := MCPServer{URL: params.Url, Headers: headers, HeaderVariables: variables, Tools: tool.Tools}
	if params.Timeout != nil {
		server.TimeoutSeconds = *params.Timeout
	}
	return server, nil
}

// expandHeaders resolves the compiler's __KAGENT_ENV[NAME]__ tokens and
// returns the variables it read, sorted. Kagent sets a Secret-backed variable
// to the egress gateway's placeholder, so the resolved value is the
// placeholder, never the Secret.
func expandHeaders(headers map[string]string, lookupEnv func(string) (string, bool)) (map[string]string, []string, error) {
	if len(headers) == 0 {
		return nil, nil, nil
	}
	result := make(map[string]string, len(headers))
	var variables []string
	for name, value := range headers {
		if strings.HasPrefix(value, envTokenPrefix) && strings.HasSuffix(value, envTokenSuffix) {
			variable := strings.TrimSuffix(strings.TrimPrefix(value, envTokenPrefix), envTokenSuffix)
			resolved, ok := lookupEnv(variable)
			if !ok {
				return nil, nil, fmt.Errorf("header %q: environment variable %s is not set", name, variable)
			}
			value = resolved
			variables = append(variables, variable)
		}
		result[name] = value
	}
	slices.Sort(variables)
	return result, slices.Compact(variables), nil
}

func requireHTTPURL(what, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL", what)
	}
	return nil
}

// uniqueServerName names a server after the first label of its host, so a
// muster server's tools read mcp__muster_* to the model. The ADK configuration
// carries no server name; a second server on the same label gets a suffix.
// The name is already in the form omp's tool names use, so the adapter can
// spell them.
func uniqueServerName(rawURL string, used map[string]int) string {
	name := "mcp"
	if u, err := url.Parse(rawURL); err == nil {
		label, _, _ := strings.Cut(u.Hostname(), ".")
		if label = strings.Trim(serverNameInvalid.ReplaceAllString(strings.ToLower(label), "_"), "_"); label != "" {
			name = label
		}
	}
	used[name]++
	if count := used[name]; count > 1 {
		return fmt.Sprintf("%s_%d", name, count)
	}
	return name
}
