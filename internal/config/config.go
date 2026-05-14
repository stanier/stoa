package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	LLM LLMConfig `mapstructure:"llm"`
	UI          UIConfig                 `mapstructure:"ui"`
	Compaction  CompactionConfig         `mapstructure:"compaction"`
	MCP         MCPConfig                `mapstructure:"mcp"`
	Agents      AgentsConfig             `mapstructure:"agents"`
	Zulip       ZulipConfig       `mapstructure:"zulip"`
	Slack       SlackConfig       `mapstructure:"slack"`
	Discord     DiscordConfig     `mapstructure:"discord"`
	Matrix      MatrixConfig      `mapstructure:"matrix"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Trace       TraceConfig       `mapstructure:"trace"`
	Prompts     PromptsConfig     `mapstructure:"prompts"`
	Permissions PermissionsConfig `mapstructure:"permissions"`
	Skills      SkillsConfig      `mapstructure:"skills"`
	// Agent daemon connection
	Agent AgentConfig `mapstructure:"agent"`

	// Agent daemon server settings
	Agentd AgentdConfig `mapstructure:"agentd"`

	// OAuth2 / user authentication
	Auth AuthConfig `mapstructure:"auth"`

	Titling TitlingConfig `mapstructure:"titling"`

	Gateway GatewayConfig `mapstructure:"gateway"`

	// Runtime flags (not from config file)
	EnableConnectorsFlag bool   // Set by --enable-connectors flag
	LoadedFrom           string // Path of the config file that was loaded; empty if none found
}

// GatewayConfig controls provider fallback and retry behaviour.
type GatewayConfig struct {
	// FallbackChain is an ordered list of provider names to try on retryable errors.
	// Provider names must match entries in LLMProviders (or "default").
	// Example: ["default", "anthropic-backup"]
	FallbackChain []string `mapstructure:"fallback_chain"`

	// RetryableStatusCodes is the list of HTTP status codes that trigger a fallback.
	// Default: [429, 500, 502, 503, 504].
	RetryableStatusCodes []int `mapstructure:"retryable_status_codes"`

	// PerProviderTimeout is the maximum time to wait for a single provider before
	// giving up and trying the next one in the chain.
	// Default: 30s.
	PerProviderTimeout time.Duration `mapstructure:"per_provider_timeout"`

	// DynamicRouting enables health-aware provider ordering. When true, the fallback
	// chain is re-sorted by error rate + P95 latency before each request instead of
	// using the static configured order. Default: false.
	DynamicRouting bool `mapstructure:"dynamic_routing"`

	// Cache configures the response cache.
	Cache GatewayCacheConfig `mapstructure:"cache"`

	// RateLimit controls per-user request velocity limiting.
	RateLimit GatewayRateLimitConfig `mapstructure:"rate_limit"`
}

// GatewayRateLimitConfig controls per-user request velocity limiting.
type GatewayRateLimitConfig struct {
	// Enabled activates per-user rate limiting. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// RequestsPerMinute is the max requests a single user may make per minute.
	// 0 means unlimited. Default: 0.
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
}

// GatewayCacheConfig controls LLM response caching.
type GatewayCacheConfig struct {
	// Enabled activates the in-memory response cache. Default: false.
	Enabled bool `mapstructure:"enabled"`

	// TTL is how long a cached response remains valid. Default: 1h.
	TTL time.Duration `mapstructure:"ttl"`

	// MaxEntries is the maximum number of entries in the memory cache.
	// 0 means unlimited. Default: 10000.
	MaxEntries int `mapstructure:"max_entries"`

	// RedisURL is reserved for a future Redis-backed cache implementation.
	// Setting this field currently logs a warning and falls back to the memory cache.
	RedisURL string `mapstructure:"redis_url"`
}

// AuthConfig controls the OAuth2 authentication system.
// When Enabled is false, agentd falls back to API key auth.
type AuthConfig struct {
	// Enabled activates the OAuth2 identity system. Requires AGENTD_DATABASE_URL.
	Enabled bool `mapstructure:"enabled"`

	// Providers lists trusted OAuth2/OIDC providers. Each must have a
	// corresponding AGENTD_OAUTH_<UPPERCASE_ID>_CLIENT_SECRET env var
	// (or AGENTD_OAUTH_CLIENT_SECRET when there is only one provider).
	Providers []OAuthProviderConfig `mapstructure:"providers"`

	// SessionTTLHours is how long session tokens remain valid.
	// Default: 24.
	SessionTTLHours int `mapstructure:"session_ttl_hours"`
}

// OAuthProviderConfig describes one trusted OAuth2/OIDC identity provider.
type OAuthProviderConfig struct {
	// ID is a short stable identifier used in logs and the users table.
	// Example: "gitlab-ce"
	ID string `mapstructure:"id"`

	// IssuerURL is the OIDC issuer base URL, used for discovery.
	// Example: "https://gitlab.example.com"
	IssuerURL string `mapstructure:"issuer_url"`

	// ClientID is the OAuth2 application client ID registered with the provider.
	ClientID string `mapstructure:"client_id"`

	// ClientSecretEnv names the environment variable that holds the client secret.
	// Defaults to AGENTD_OAUTH_CLIENT_SECRET (single-provider) or
	// AGENTD_OAUTH_<UPPERCASE_ID>_CLIENT_SECRET (multi-provider).
	ClientSecretEnv string `mapstructure:"client_secret_env"`
}

// AgentConfig holds configuration for connecting to the agentd daemon.
type AgentConfig struct {
	// Address is the base URL of the agentd HTTP/WebSocket server.
	// Example: "http://localhost:7777"
	Address string `mapstructure:"address"`

	// APIKey is the Bearer token sent to agentd for authentication.
	// Corresponds to agentd's --api-key / AGENTD_API_KEY setting.
	// Empty means no Authorization header is sent.
	APIKey string `mapstructure:"api_key"`

	// TLSSkipVerify disables TLS certificate verification when connecting to agentd.
	// Only set this in development or when using a self-signed cert.
	TLSSkipVerify bool `mapstructure:"tls_skip_verify"`
}

// AgentdConfig holds configuration for the agentd daemon server.
type AgentdConfig struct {
	// SessionDir is the directory where session state is persisted across restarts.
	// Empty string disables persistence.
	SessionDir string `mapstructure:"session_dir"`

	// SquidConfPath is the filesystem path where agentd writes the generated squid.conf.
	// Should match the bind-mount source path in docker-compose.yml.
	// Empty string disables squid config generation.
	SquidConfPath string `mapstructure:"squid_conf_path"`

	// APIKeydURL is the internal URL of the apikeyd service used to validate
	// external ACP caller API keys (stoa-infgw_ prefix).
	// Example: "http://apikeyd:8889"
	// When empty, apikeyd-based key validation is disabled.
	APIKeydURL string `mapstructure:"apikeyd_url"`
}

// TitlingConfig controls automatic conversation title generation.
type TitlingConfig struct {
	// Enabled turns on per-turn title generation. Default false.
	Enabled bool `mapstructure:"enabled"`
	// Provider is the name of the provider from the registry to use for titling.
	// Empty string means use the default server provider.
	Provider string `mapstructure:"provider"`
	// Model is the model ID to use. Empty means the provider's default.
	// A fast/cheap model is recommended (e.g. gpt-4o-mini, claude-haiku-4-5-20251001).
	Model string `mapstructure:"model"`
	// Prompt overrides the built-in titling system prompt.
	Prompt string `mapstructure:"prompt"`
	// DisableThinking suppresses extended thinking for the titling call.
	// Default true — titling is a cheap summarisation task that doesn't benefit
	// from reasoning. Set to false to let the selected provider's own
	// enable_thinking setting apply instead.
	DisableThinking bool `mapstructure:"disable_thinking"`
}

// LLMProviderConfig describes one named LLM provider.
// All providers — including the default — are defined under llm.providers in the YAML config.
type LLMProviderConfig struct {
	// Name is a unique identifier used when selecting a provider. e.g. "inference-gateway", "anthropic-opus"
	Name string `mapstructure:"name"`
	// Type is "openai" (or any OpenAI-compatible endpoint), "anthropic", or "gemini".
	Type string `mapstructure:"type"`
	// APIURL is the literal base URL for the provider endpoint.
	// Leave empty and use api_url_env to inject the URL from the environment.
	APIURL string `mapstructure:"api_url"`
	// APIURLEnv names the environment variable that holds the API endpoint URL.
	// Used (and wins over api_url) when non-empty.
	APIURLEnv string `mapstructure:"api_url_env"`
	// APIKey is the literal API key. Use api_key_env instead to avoid secrets in config files.
	APIKey string `mapstructure:"api_key"`
	// APIKeyEnv names the environment variable that holds the API key.
	// If unset, falls back to a provider-specific name (e.g. ANTHROPIC_API_KEY).
	APIKeyEnv string `mapstructure:"api_key_env"`
	// Model is the default/active model for this provider.
	Model string `mapstructure:"model"`
	// Models is the static list of available model IDs for this provider.
	// When empty, models are auto-discovered from /v1/models at startup.
	Models []string `mapstructure:"models"`
	// ModelRefreshInterval controls how often model discovery is repeated.
	// Default: 0 (discover only at startup).
	ModelRefreshInterval string `mapstructure:"model_refresh_interval"`
	// MaxTokens is the maximum number of tokens in the response.
	MaxTokens int `mapstructure:"max_tokens"`
	// TLSSkipVerify disables TLS certificate verification for this provider.
	TLSSkipVerify bool `mapstructure:"tls_skip_verify"`
	// Sampling parameters — only sent when explicitly set.
	Temperature          *float64 `mapstructure:"temperature"`
	TopP                 *float64 `mapstructure:"top_p"`
	TopK                 *int     `mapstructure:"top_k"`             // Anthropic only
	PresencePenalty      *float64 `mapstructure:"presence_penalty"`  // OpenAI only
	FrequencyPenalty     *float64 `mapstructure:"frequency_penalty"` // OpenAI only
	Stop                 []string `mapstructure:"stop"`
	EnableThinking       *bool    `mapstructure:"enable_thinking"`        // Anthropic native; OpenAI passed as extra body fields
	ThinkingBudgetTokens *int     `mapstructure:"thinking_budget_tokens"` // Anthropic native; OpenAI passed as extra body fields
}

// LLMConfig holds the multi-provider LLM configuration.
type LLMConfig struct {
	// Default is the name of the provider to use when no override is requested.
	// When empty, the first entry in Providers is used.
	Default   string              `mapstructure:"default"`
	Providers []LLMProviderConfig `mapstructure:"providers"`
}

// DefaultProvider returns the provider designated by LLM.Default, falling back
// to the first provider when Default is unset or not found.
// Returns nil when no providers are configured.
func (c *LLMConfig) DefaultProvider() *LLMProviderConfig {
	if len(c.Providers) == 0 {
		return nil
	}
	if c.Default != "" {
		for i := range c.Providers {
			if c.Providers[i].Name == c.Default {
				return &c.Providers[i]
			}
		}
	}
	return &c.Providers[0]
}

// GetProvider returns the provider with the given name, or nil if not found.
func (c *LLMConfig) GetProvider(name string) *LLMProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// UIConfig holds UI-related configuration
type UIConfig struct {
	Theme            string `mapstructure:"theme"`
	ShowTimestamps   bool   `mapstructure:"show_timestamps"`
	HideSystemPrompt bool   `mapstructure:"hide_system_prompt"`
	Persona          string `mapstructure:"persona"`
}

// CompactionConfig holds compaction-related configuration
type CompactionConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	MaxTokens        int    `mapstructure:"max_tokens"`
	CompactThreshold int    `mapstructure:"compact_threshold"`
	Strategy         string `mapstructure:"strategy"`
	KeepRecent       int    `mapstructure:"keep_recent"`
}

// MCPConfig holds MCP server configuration
type MCPConfig struct {
	Enabled      bool              `mapstructure:"enabled"`
	Servers      []MCPServerConfig `mapstructure:"servers"`
	ProxyAddress string            `mapstructure:"proxy_address"` // HTTP proxy for containers with allowed_domains (e.g. "http://stoa-squid:3128")
}

// PermissionPolicyConfig represents an inline permission policy declared in config
type PermissionPolicyConfig struct {
	ToolPattern string `mapstructure:"tool_pattern"` // Glob pattern for tool names (e.g. "mcp_*_read_*")
	Rank        int    `mapstructure:"rank"`         // 0=Silent, 1=Mention, 2=Request
	Reason      string `mapstructure:"reason"`       // Human-readable explanation
}

// MCPServerConfig represents configuration for an MCP server
type MCPServerConfig struct {
	Enabled   *bool                    `mapstructure:"enabled"`   // nil means enabled (default true)
	Name      string                   `mapstructure:"name"`
	Transport string                   `mapstructure:"transport"` // "stdio", "http", or "docker"
	Command   string                   `mapstructure:"command"`   // For stdio transport
	Args      []string                 `mapstructure:"args"`      // For stdio and docker transports
	URL       string                   `mapstructure:"url"`       // For HTTP transport
	Image       string                   `mapstructure:"image"`        // For Docker transport
	DockerHost  string                   `mapstructure:"docker_host"`  // For Docker transport (e.g. unix:///run/user/1000/podman/podman.sock)
	Volumes     []VolumeConfig           `mapstructure:"volumes"`      // For Docker transport
	NetworkMode    string                   `mapstructure:"network_mode"`    // For Docker transport
	Env            map[string]string        `mapstructure:"env"`             // Environment variables
	AllowedDomains []string                 `mapstructure:"allowed_domains"` // For Docker transport: restrict outbound to these domains via proxy (empty = allow all public)
	ProxySubnet    string                   `mapstructure:"proxy_subnet"`    // For Docker transport: CIDR of the container's isolated network (e.g. "172.28.1.0/24"); enables proxy injection
	Policies       []PermissionPolicyConfig `mapstructure:"policies"`        // Inline permission policies
}

// VolumeConfig represents a Docker volume mount
type VolumeConfig struct {
	Type     string `mapstructure:"type"`      // "bind" or "volume"
	Source   string `mapstructure:"source"`    // Host path or volume name
	Target   string `mapstructure:"target"`    // Container path
	ReadOnly bool   `mapstructure:"read_only"` // Read-only mount
}

// AgentsConfig holds agent-related configuration
type AgentsConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	MaxConcurrent int  `mapstructure:"max_concurrent"`
	ShareContext  bool `mapstructure:"share_context"`
}

// DiscordRolePolicy describes what a Discord user in a given role may do.
type DiscordRolePolicy struct {
	Allow        bool     `mapstructure:"allow"`
	Persona      string   `mapstructure:"persona"`
	AllowedTools []string `mapstructure:"allowed_tools"`
}

// DiscordAccessPolicy configures access control for Discord messages.
type DiscordAccessPolicy struct {
	Unauthenticated DiscordRolePolicy `mapstructure:"unauthenticated"`
}

// DiscordConfig holds Discord integration configuration.
type DiscordConfig struct {
	Enabled       bool                `mapstructure:"enabled"`
	BotToken      string              `mapstructure:"bot_token"`
	CommandPrefix string              `mapstructure:"command_prefix"` // default "!"
	Persona       string              `mapstructure:"persona"`
	SessionTTL    time.Duration       `mapstructure:"session_ttl"`
	SessionFile   string              `mapstructure:"session_file"`
	AccessPolicy  DiscordAccessPolicy `mapstructure:"access_policy"`
}

// MatrixRolePolicy describes what a Matrix user in a given role may do.
type MatrixRolePolicy struct {
	Allow        bool     `mapstructure:"allow"`
	Persona      string   `mapstructure:"persona"`
	AllowedTools []string `mapstructure:"allowed_tools"`
}

// MatrixAccessPolicy configures access control for Matrix messages.
type MatrixAccessPolicy struct {
	Unauthenticated MatrixRolePolicy `mapstructure:"unauthenticated"`
}

// MatrixConfig holds Matrix integration configuration.
type MatrixConfig struct {
	Enabled       bool               `mapstructure:"enabled"`
	HomeserverURL string             `mapstructure:"homeserver_url"`
	Username      string             `mapstructure:"username"`      // @bot:server.com
	Password      string             `mapstructure:"password"`      // optional: use instead of access_token
	AccessToken   string             `mapstructure:"access_token"`  // optional: use instead of password
	CommandPrefix string             `mapstructure:"command_prefix"` // default "!"
	Persona       string             `mapstructure:"persona"`
	SessionTTL    time.Duration      `mapstructure:"session_ttl"`
	SessionFile   string             `mapstructure:"session_file"`
	AccessPolicy  MatrixAccessPolicy `mapstructure:"access_policy"`
}

// SlackRolePolicy describes what a Slack user in a given role may do.
type SlackRolePolicy struct {
	// Allow controls whether users in this role may interact with the bot at all.
	Allow bool `mapstructure:"allow"`
	// Persona is the agentd persona to use for sessions created for this role.
	Persona string `mapstructure:"persona"`
	// AllowedTools is a list of glob patterns restricting which tools the bot
	// may use in sessions for this role. Empty means use the persona's full tool set.
	AllowedTools []string `mapstructure:"allowed_tools"`
}

// SlackAccessPolicy configures access control for Slack messages.
type SlackAccessPolicy struct {
	// Unauthenticated applies to senders whose Slack account is not linked to
	// an authd account, or whose linked account has no active session.
	Unauthenticated SlackRolePolicy `mapstructure:"unauthenticated"`
}

// SlackConfig holds Slack integration configuration.
type SlackConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	BotToken      string        `mapstructure:"bot_token"`      // xoxb-*
	AppToken      string        `mapstructure:"app_token"`      // xapp-*
	CommandPrefix string        `mapstructure:"command_prefix"` // default "!"
	Persona       string        `mapstructure:"persona"`
	SessionTTL    time.Duration `mapstructure:"session_ttl"`  // default 24h
	SessionFile   string        `mapstructure:"session_file"` // persistence path
	AccessPolicy  SlackAccessPolicy `mapstructure:"access_policy"`
}

// ZulipRolePolicy describes what a Zulip user in a given role may do.
type ZulipRolePolicy struct {
	// Allow controls whether users in this role may interact with the bot at all.
	// Defaults to false for the unauthenticated role, true for named roles.
	Allow bool `mapstructure:"allow"`
	// Persona is the agentd persona to use for sessions created for this role.
	// Empty string means use the global zulip.persona default.
	Persona string `mapstructure:"persona"`
	// AllowedTools is a list of glob patterns restricting which tools the bot
	// may use in sessions for this role. Empty means use the persona's full tool set.
	AllowedTools []string `mapstructure:"allowed_tools"`
}

// ZulipAccessPolicy configures access control for Zulip messages.
// Roles for authenticated users are managed in the authd database via
// auth-admin authd role commands.
type ZulipAccessPolicy struct {
	// Unauthenticated applies to senders whose Zulip account is not linked to
	// an authd account, or whose linked account has no active session.
	Unauthenticated ZulipRolePolicy `mapstructure:"unauthenticated"`
}

// ZulipConfig holds Zulip integration configuration
type ZulipConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	SiteURL         string   `mapstructure:"site_url"`
	Email           string   `mapstructure:"email"`
	APIKey          string   `mapstructure:"api_key"`
	EventTypes      []string `mapstructure:"event_types"`
	UseReactions    bool     `mapstructure:"use_reactions"`
	EmergencyCutoff string   `mapstructure:"emergency_cutoff"`
	CommandPrefix   string        `mapstructure:"command_prefix"` // Prefix for commands (default "!")
	Persona         string        `mapstructure:"persona"`
	SessionTTL      time.Duration `mapstructure:"session_ttl"`   // Idle session expiry (default: 24h)
	SessionFile     string        `mapstructure:"session_file"`  // Persistence path
	// AllowedStreams is the list of Zulip stream names the bot responds to freely.
	// Messages from streams not in this list are only processed when the bot is @-mentioned.
	// An empty list means all streams are allowed (backward compatible default).
	AllowedStreams  []string      `mapstructure:"allowed_streams"`
	AccessPolicy    ZulipAccessPolicy `mapstructure:"access_policy"`
}

// LoggingConfig holds logging-related configuration
type LoggingConfig struct {
	Level   string `mapstructure:"level"`    // debug, info, warn, error
	LogFile string `mapstructure:"log_file"` // Path to log file (empty = no file logging)
}

// TraceConfig holds conversation tracing configuration
type TraceConfig struct {
	Enabled bool   `mapstructure:"enabled"` // Enable conversation tracing
	Dir     string `mapstructure:"dir"`     // Directory for trace files
}

// PromptsConfig holds prompt management configuration
type PromptsConfig struct {
	Enabled     bool   `mapstructure:"enabled"`      // Enable prompt system
	OverrideDir string `mapstructure:"override_dir"` // Directory for custom prompt overrides
}

// PermissionsConfig holds MCP tool permissions configuration
type PermissionsConfig struct {
	Enabled          bool          `mapstructure:"enabled"`            // Enable permission system
	PolicyFile       string        `mapstructure:"policy_file"`        // Path to permissions policy file
	DefaultRank      int           `mapstructure:"default_rank"`       // Default permission rank (0-5)
	AuditLog         bool          `mapstructure:"audit_log"`          // Enable audit logging
	AuditLogPath     string        `mapstructure:"audit_log_path"`     // Path to audit log file
	AutoApproveRank2 bool          `mapstructure:"auto_approve_rank2"` // Auto-approve Rank 2 (Request) permissions in headless mode
	ApprovalTimeout  time.Duration `mapstructure:"approval_timeout"`   // How long to wait for a Zulip reaction before denying (default 60s)
}

// SkillsConfig holds configuration for built-in tools
type SkillsConfig struct {
	EnabledBuiltin []string `mapstructure:"enabled_builtin"`
	Directories    []string `mapstructure:"directories"`
}

// PersonaConfig holds full configuration for a specific persona
type PersonaConfig struct {
	Name                    string   `mapstructure:"name"`
	Description             string   `mapstructure:"description"`
	SystemPrompt            string   `mapstructure:"system_prompt"`
	Capabilities            []string `mapstructure:"capabilities"`
	Tools                   []string `mapstructure:"tools"`
	MaxConcurrentTasks      int      `mapstructure:"max_concurrent_tasks"`
	ThinkingStyle           string   `mapstructure:"thinking_style"`           // fast, deep, creative, precise
	CommunicationPreference string   `mapstructure:"communication_preference"` // verbose, terse, silent
}

// Default values
const (
	DefaultProvider       = "openai"
	DefaultOpenAIURL      = "https://api.openai.com/v1"
	DefaultAnthropicURL   = "https://api.anthropic.com"
	DefaultOpenAIModel    = "gpt-4o"
	DefaultAnthropicModel = "claude-sonnet-4-5-20250929"
	DefaultMaxTokens      = 4096
	DefaultTheme          = "dark"

	// Compaction defaults
	DefaultCompactionEnabled    = true
	DefaultCompactionMaxTokens  = 100000
	DefaultCompactionThreshold  = 80000
	DefaultCompactionStrategy   = "sliding_window"
	DefaultCompactionKeepRecent = 10
)

// Load loads configuration from file and environment variables
func Load() (*Config, error) {
	v := viper.New()

	// Set initial defaults
	v.SetDefault("ui.theme", DefaultTheme)
	v.SetDefault("ui.show_timestamps", true)
	v.SetDefault("ui.hide_system_prompt", true)

	// Compaction defaults
	v.SetDefault("compaction.enabled", DefaultCompactionEnabled)
	v.SetDefault("compaction.max_tokens", DefaultCompactionMaxTokens)
	v.SetDefault("compaction.compact_threshold", DefaultCompactionThreshold)
	v.SetDefault("compaction.strategy", DefaultCompactionStrategy)
	v.SetDefault("compaction.keep_recent", DefaultCompactionKeepRecent)

	// MCP defaults
	v.SetDefault("mcp.enabled", false)
	v.SetDefault("mcp.servers", []MCPServerConfig{})

	// Agents defaults
	v.SetDefault("agents.enabled", true)
	v.SetDefault("agents.max_concurrent", 5)
	v.SetDefault("agents.share_context", true)

	// UI defaults
	v.SetDefault("ui.persona", "default")

	// Zulip defaults
	v.SetDefault("zulip.enabled", false)
	v.SetDefault("zulip.event_types", []string{"message"})
	v.SetDefault("zulip.use_reactions", true)
	v.SetDefault("zulip.command_prefix", "!")
	v.SetDefault("zulip.persona", "default")
	v.SetDefault("zulip.session_ttl", 24*time.Hour)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	logDir, _ := GetConfigDir()
	v.SetDefault("logging.log_file", filepath.Join(logDir, "chat.log"))
	v.SetDefault("zulip.session_file", filepath.Join(logDir, "zulip-sessions.json"))

	// Prompts defaults
	v.SetDefault("prompts.enabled", true)
	promptsDir := filepath.Join(logDir, "prompts")
	v.SetDefault("prompts.override_dir", promptsDir)

	// Trace defaults
	v.SetDefault("trace.enabled", true)
	traceDir := filepath.Join(logDir, "traces")
	v.SetDefault("trace.dir", traceDir)

	// Permissions defaults
	v.SetDefault("permissions.enabled", true)
	v.SetDefault("permissions.policy_file", filepath.Join(logDir, "permissions.yaml"))
	v.SetDefault("permissions.default_rank", 2) // RankRequest - conservative default
	v.SetDefault("permissions.audit_log", true)
	v.SetDefault("permissions.audit_log_path", filepath.Join(logDir, "permissions-audit.jsonl"))
	v.SetDefault("permissions.approval_timeout", 60*time.Second)

	// Skills defaults
	v.SetDefault("skills.enabled_builtin", []string{})
	v.SetDefault("skills.directories", []string{"~/.claude/skills", ".claude/skills"})
	v.BindEnv("skills.directories", "SKILLS_DIRS")

	// Agent daemon defaults
	v.SetDefault("agent.address", "http://localhost:7777")
	v.BindEnv("agent.address", "AGENT_ADDRESS")
	v.BindEnv("agent.api_key", "AGENTD_API_KEY")
	v.BindEnv("agent.tls_skip_verify", "AGENTD_TLS_SKIP_VERIFY")

	// Bind environment variables for trace
	v.BindEnv("trace.enabled", "TRACE_ENABLED")
	v.BindEnv("trace.dir", "TRACE_DIR")

	// Bind environment variables for permissions
	v.BindEnv("permissions.enabled", "PERMISSIONS_ENABLED")
	v.BindEnv("permissions.policy_file", "PERMISSIONS_POLICY_FILE")
	v.BindEnv("permissions.default_rank", "PERMISSIONS_DEFAULT_RANK")
	v.BindEnv("permissions.audit_log", "PERMISSIONS_AUDIT_LOG")
	v.BindEnv("permissions.audit_log_path", "PERMISSIONS_AUDIT_LOG_PATH")

	// Bind environment variables for prompts
	v.BindEnv("prompts.enabled", "PROMPTS_ENABLED")
	v.BindEnv("prompts.override_dir", "PROMPTS_DIR")

	// Titling defaults — all titling options come from YAML only.
	v.SetDefault("titling.enabled", false)
	v.SetDefault("titling.disable_thinking", true)

	// Gateway defaults
	v.SetDefault("gateway.retryable_status_codes", []int{429, 500, 502, 503, 504})
	v.SetDefault("gateway.per_provider_timeout", 30*time.Second)
	v.BindEnv("gateway.fallback_chain", "GATEWAY_FALLBACK_CHAIN")
	v.SetDefault("gateway.dynamic_routing", false)
	v.BindEnv("gateway.dynamic_routing", "GATEWAY_DYNAMIC_ROUTING")

	// Gateway cache defaults
	v.SetDefault("gateway.cache.enabled", false)
	v.SetDefault("gateway.cache.ttl", time.Hour)
	v.SetDefault("gateway.cache.max_entries", 10000)
	v.BindEnv("gateway.cache.enabled", "GATEWAY_CACHE_ENABLED")
	v.BindEnv("gateway.cache.ttl", "GATEWAY_CACHE_TTL")
	v.BindEnv("gateway.cache.max_entries", "GATEWAY_CACHE_MAX_ENTRIES")
	v.BindEnv("gateway.cache.redis_url", "GATEWAY_CACHE_REDIS_URL")

	// Gateway rate limit defaults and env bindings.
	v.SetDefault("gateway.rate_limit.enabled", false)
	v.SetDefault("gateway.rate_limit.requests_per_minute", 0)
	v.BindEnv("gateway.rate_limit.enabled", "GATEWAY_RATE_LIMIT_ENABLED")
	v.BindEnv("gateway.rate_limit.requests_per_minute", "GATEWAY_RATE_LIMIT_REQUESTS_PER_MINUTE")

	// Bind agentd environment variables
	v.BindEnv("agentd.session_dir", "AGENTD_SESSION_DIR")
	v.BindEnv("agentd.squid_conf_path", "AGENTD_SQUID_CONF_PATH")
	v.BindEnv("agentd.apikeyd_url", "AGENTD_APIKEYD_URL")
	v.BindEnv("mcp.proxy_address", "MCP_PROXY_ADDRESS")

	// Bind Zulip environment variables
	v.BindEnv("zulip.enabled", "ZULIP_ENABLED")
	v.BindEnv("zulip.site_url", "ZULIP_SITE_URL")
	v.BindEnv("zulip.email", "ZULIP_EMAIL")
	v.BindEnv("zulip.api_key", "ZULIP_API_KEY")
	v.BindEnv("zulip.command_prefix", "ZULIP_COMMAND_PREFIX")
	v.BindEnv("zulip.session_ttl", "ZULIP_SESSION_TTL")
	v.BindEnv("zulip.session_file", "ZULIP_SESSION_FILE")

	// Slack defaults
	v.SetDefault("slack.enabled", false)
	v.SetDefault("slack.command_prefix", "!")
	v.SetDefault("slack.persona", "default")
	v.SetDefault("slack.session_ttl", 24*time.Hour)
	v.SetDefault("slack.session_file", filepath.Join(logDir, "slack-sessions.json"))

	// Discord defaults
	v.SetDefault("discord.enabled", false)
	v.SetDefault("discord.command_prefix", "!")
	v.SetDefault("discord.persona", "default")
	v.SetDefault("discord.session_ttl", 24*time.Hour)
	v.SetDefault("discord.session_file", filepath.Join(logDir, "discord-sessions.json"))

	// Bind Discord environment variables
	v.BindEnv("discord.enabled", "DISCORD_ENABLED")
	v.BindEnv("discord.bot_token", "DISCORD_BOT_TOKEN")
	v.BindEnv("discord.command_prefix", "DISCORD_COMMAND_PREFIX")
	v.BindEnv("discord.persona", "DISCORD_PERSONA")
	v.BindEnv("discord.session_ttl", "DISCORD_SESSION_TTL")
	v.BindEnv("discord.session_file", "DISCORD_SESSION_FILE")

	// Matrix defaults
	v.SetDefault("matrix.enabled", false)
	v.SetDefault("matrix.command_prefix", "!")
	v.SetDefault("matrix.persona", "default")
	v.SetDefault("matrix.session_ttl", 24*time.Hour)
	v.SetDefault("matrix.session_file", filepath.Join(logDir, "matrix-sessions.json"))

	// Bind Matrix environment variables
	v.BindEnv("matrix.enabled", "MATRIX_ENABLED")
	v.BindEnv("matrix.homeserver_url", "MATRIX_HOMESERVER_URL")
	v.BindEnv("matrix.username", "MATRIX_USERNAME")
	v.BindEnv("matrix.password", "MATRIX_PASSWORD")
	v.BindEnv("matrix.access_token", "MATRIX_ACCESS_TOKEN")
	v.BindEnv("matrix.command_prefix", "MATRIX_COMMAND_PREFIX")
	v.BindEnv("matrix.persona", "MATRIX_PERSONA")
	v.BindEnv("matrix.session_ttl", "MATRIX_SESSION_TTL")
	v.BindEnv("matrix.session_file", "MATRIX_SESSION_FILE")

	// Bind Slack environment variables
	v.BindEnv("slack.enabled", "SLACK_ENABLED")
	v.BindEnv("slack.bot_token", "SLACK_BOT_TOKEN")
	v.BindEnv("slack.app_token", "SLACK_APP_TOKEN")
	v.BindEnv("slack.command_prefix", "SLACK_COMMAND_PREFIX")
	v.BindEnv("slack.persona", "SLACK_PERSONA")
	v.BindEnv("slack.session_ttl", "SLACK_SESSION_TTL")
	v.BindEnv("slack.session_file", "SLACK_SESSION_FILE")

	// Config file — search in priority order: system, user home, then repo-local
	// config/ dir (convenient when running from the repo root during development).
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/stoa")

	configDir, err := GetConfigDir()
	if err == nil {
		v.AddConfigPath(configDir)
	}

	v.AddConfigPath(".stoa/config") // $PWD/.stoa/config/config.yaml for per-project overrides

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Unmarshal into struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}
	cfg.LoadedFrom = v.ConfigFileUsed()

	// Viper lowercases all map keys; MCP server env var names must be
	// uppercase so the container process can find them via os.Getenv.
	for i := range cfg.MCP.Servers {
		if len(cfg.MCP.Servers[i].Env) == 0 {
			continue
		}
		fixed := make(map[string]string, len(cfg.MCP.Servers[i].Env))
		for k, v := range cfg.MCP.Servers[i].Env {
			fixed[strings.ToUpper(k)] = v
		}
		cfg.MCP.Servers[i].Env = fixed
	}

	// resolveFileSecret reads a secret value from a file path stored in an env var.
	// Used for Podman/Docker secrets (e.g. ANTHROPIC_API_KEY_FILE=/run/secrets/api_key).
	resolveFileSecret := func(fileEnv, current string) string {
		path := os.Getenv(fileEnv)
		if path == "" {
			return current
		}
		data, err := os.ReadFile(strings.TrimSpace(path))
		if err != nil {
			return current
		}
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
		return current
	}

	// Apply per-provider defaults and resolve env vars for credentials/endpoints.
	for i := range cfg.LLM.Providers {
		pc := &cfg.LLM.Providers[i]

		if pc.Type == "" {
			pc.Type = DefaultProvider
		}
		if pc.MaxTokens <= 0 {
			pc.MaxTokens = DefaultMaxTokens
		}

		// Determine which env var names to use for the API key and URL.
		keyEnv := pc.APIKeyEnv
		if keyEnv == "" {
			if pc.Type == "anthropic" {
				keyEnv = "ANTHROPIC_API_KEY"
			} else {
				keyEnv = "OPENAI_API_KEY"
			}
		}
		urlEnv := pc.APIURLEnv
		if urlEnv == "" {
			if pc.Type == "anthropic" {
				urlEnv = "ANTHROPIC_API_URL"
			} else {
				urlEnv = "OPENAI_API_URL"
			}
		}

		// Env var wins over any literal api_key / api_url set in YAML.
		if v := os.Getenv(keyEnv); v != "" {
			pc.APIKey = v
		}
		pc.APIKey = resolveFileSecret(keyEnv+"_FILE", pc.APIKey)

		if v := os.Getenv(urlEnv); v != "" {
			pc.APIURL = v
		}
		if pc.APIURL == "" {
			if pc.Type == "anthropic" {
				pc.APIURL = DefaultAnthropicURL
			} else {
				pc.APIURL = DefaultOpenAIURL
			}
		}

		if pc.Model == "" {
			if pc.Type == "anthropic" {
				pc.Model = DefaultAnthropicModel
			} else {
				pc.Model = DefaultOpenAIModel
			}
		}
	}

	// GATEWAY_FALLBACK_CHAIN is a comma-separated string; split it if the env var
	// produced a single-element slice containing commas (Viper doesn't auto-split).
	if raw := os.Getenv("GATEWAY_FALLBACK_CHAIN"); raw != "" && len(cfg.Gateway.FallbackChain) == 1 && strings.Contains(cfg.Gateway.FallbackChain[0], ",") {
		parts := strings.Split(cfg.Gateway.FallbackChain[0], ",")
		trimmed := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				trimmed = append(trimmed, s)
			}
		}
		cfg.Gateway.FallbackChain = trimmed
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	dp := c.LLM.DefaultProvider()

	// When connecting to agentd, LLM credentials are not required on the client side.
	if c.Agent.Address != "" && (dp == nil || dp.APIKey == "") {
		return nil
	}

	if dp == nil {
		return fmt.Errorf("no LLM providers configured; add at least one entry under llm.providers")
	}

	if dp.APIKey == "" {
		keyEnv := dp.APIKeyEnv
		if keyEnv == "" {
			if dp.Type == "anthropic" {
				keyEnv = "ANTHROPIC_API_KEY"
			} else {
				keyEnv = "OPENAI_API_KEY"
			}
		}
		return fmt.Errorf("API key is required: set %s, or set api_key / api_key_env for provider %q in the YAML config", keyEnv, dp.Name)
	}

	if dp.APIURL == "" {
		return fmt.Errorf("API URL is required for provider %q", dp.Name)
	}

	if dp.Model == "" {
		return fmt.Errorf("model is required for provider %q", dp.Name)
	}

	if dp.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be positive for provider %q", dp.Name)
	}

	if dp.Temperature != nil && (*dp.Temperature < 0 || *dp.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0 and 2 for provider %q", dp.Name)
	}

	if dp.TopP != nil && (*dp.TopP < 0 || *dp.TopP > 1) {
		return fmt.Errorf("top_p must be between 0 and 1 for provider %q", dp.Name)
	}

	if dp.TopK != nil && *dp.TopK < 0 {
		return fmt.Errorf("top_k must be non-negative for provider %q", dp.Name)
	}

	if dp.PresencePenalty != nil && (*dp.PresencePenalty < -2 || *dp.PresencePenalty > 2) {
		return fmt.Errorf("presence_penalty must be between -2 and 2 for provider %q", dp.Name)
	}

	if dp.FrequencyPenalty != nil && (*dp.FrequencyPenalty < -2 || *dp.FrequencyPenalty > 2) {
		return fmt.Errorf("frequency_penalty must be between -2 and 2 for provider %q", dp.Name)
	}

	return nil
}

// GetConfigDir returns the configuration directory path
func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	configDir := filepath.Join(home, ".config", "stoa")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}

	return configDir, nil
}

// CreateDefaultConfig creates a default config file in the config directory
func CreateDefaultConfig() error {
	configDir, err := GetConfigDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(configDir, "config.yaml")

	// Don't overwrite existing config
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("config file already exists at %s", configPath)
	}

	defaultConfig := `# Stoa Configuration

llm:
  # default names the provider to use when no session override is active.
  # When omitted, the first entry in providers is used.
  default: openai

  providers:
    - name: openai
      # type: "openai" (or any OpenAI-compatible endpoint) or "anthropic"
      type: openai
      # Model to use — set here in YAML.
      model: gpt-4o
      max_tokens: 4096
      # api_key_env names the env var that holds the API key (set in .env).
      # Defaults to OPENAI_API_KEY when type is openai.
      api_key_env: OPENAI_API_KEY
      # api_url_env names the env var that holds the endpoint URL (optional).
      # Omit to use the provider's public endpoint.
      # api_url_env: OPENAI_API_URL

      # Optional sampling parameters (uncomment to enable)
      # temperature: 1.0              # 0.0–2.0
      # top_p: 0.9                    # 0.0–1.0
      # presence_penalty: 0.0         # -2.0–2.0 (OpenAI only)
      # frequency_penalty: 0.0        # -2.0–2.0 (OpenAI only)
      # stop: ["</response>", "END"]

# titling:
#   enabled: true
#   provider: ""   # name from llm.providers; empty = use default
#   model: ""      # model ID; empty = provider default (use a fast/cheap model)
#   prompt: ""     # override the built-in titling system prompt
#   disable_thinking: true  # suppress extended thinking (default true); set false to let provider's enable_thinking apply

ui:
  theme: dark
  show_timestamps: true
  hide_system_prompt: true
`

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0600); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}
