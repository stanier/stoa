package app

import (
	"os"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/permissions"
)

const (
	// permissionRequestChannelBuffer is the buffer size for permission request channels.
	permissionRequestChannelBuffer = 10
	// permissionMentionChannelBuffer is the buffer size for permission mention channels.
	permissionMentionChannelBuffer = 50
)

// PermissionsSetup holds all permission-related components.
type PermissionsSetup struct {
	Checker     *permissions.DefaultChecker
	Manager     *permissions.DefaultManager
	AuditLogger *permissions.AuditLogger
	RequestChan chan *permissions.PermissionRequest
	MentionChan chan *permissions.PermissionMention
}

// SetupPermissions creates and initializes the permission system.
// Returns nil if config is nil or permissions are disabled.
func SetupPermissions(cfg *config.Config) *PermissionsSetup {
	if cfg == nil || !cfg.Permissions.Enabled {
		return nil
	}

	store := permissions.NewFileStore(cfg.Permissions.PolicyFile)
	manager := permissions.NewDefaultManager(store)

	if err := manager.Load(); err != nil {
		logging.Warnf("Failed to load permission policies: %v", err)
	}

	if _, err := os.Stat(cfg.Permissions.PolicyFile); os.IsNotExist(err) {
		logging.Infof("Installing default permission policies to %s", cfg.Permissions.PolicyFile)
		InstallDefaultPolicies(manager)
	}

	ApplyInlinePolicies(cfg, manager)

	defaultRank := permissions.PermissionRank(cfg.Permissions.DefaultRank)
	checker := permissions.NewDefaultChecker(manager, defaultRank)

	if err := checker.LoadGrants(store); err != nil {
		logging.Warnf("Failed to load permission grants: %v", err)
	}

	auditLogger, err := permissions.NewAuditLogger(
		cfg.Permissions.AuditLogPath,
		cfg.Permissions.AuditLog,
	)
	if err != nil {
		logging.Warnf("Failed to initialize audit logger: %v", err)
		auditLogger = nil
	}

	requestChan := make(chan *permissions.PermissionRequest, permissionRequestChannelBuffer)
	mentionChan := make(chan *permissions.PermissionMention, permissionMentionChannelBuffer)

	logging.Infof("Permission system enabled (default rank: %s)", defaultRank.String())

	return &PermissionsSetup{
		Checker:     checker,
		Manager:     manager,
		AuditLogger: auditLogger,
		RequestChan: requestChan,
		MentionChan: mentionChan,
	}
}

// DefaultPermissionPolicies returns the standard baseline permission policies.
func DefaultPermissionPolicies() []permissions.PermissionPolicy {
	return []permissions.PermissionPolicy{
		{ToolPattern: "mcp_*_file_read", Rank: permissions.RankMention, Reason: "File read operations should be visible for transparency"},
		{ToolPattern: "mcp_*_read_file", Rank: permissions.RankMention, Reason: "File read operations should be visible for transparency"},
		{ToolPattern: "mcp_*_file_write", Rank: permissions.RankRequest, Reason: "File write operations require explicit permission"},
		{ToolPattern: "mcp_*_write_file", Rank: permissions.RankRequest, Reason: "File write operations require explicit permission"},
		{ToolPattern: "mcp_*_file_delete", Rank: permissions.RankRequest, Reason: "File delete operations require explicit permission"},
		{ToolPattern: "mcp_*_delete_file", Rank: permissions.RankRequest, Reason: "File delete operations require explicit permission"},
		{ToolPattern: "mcp_*_search*", Rank: permissions.RankSilent, Reason: "Search operations are read-only and safe"},
		{ToolPattern: "mcp_*_fetch*", Rank: permissions.RankMention, Reason: "Web requests should be visible"},
		{ToolPattern: "mcp_*_hf_*", Rank: permissions.RankSilent, Reason: "Hugging Face API is read-only"},
		{ToolPattern: "*", Rank: permissions.RankRequest, Reason: "Default: require permission for unrecognized tools"},
	}
}

// InstallDefaultPolicies installs DefaultPermissionPolicies into manager and saves them.
func InstallDefaultPolicies(manager permissions.Manager) {
	for _, policy := range DefaultPermissionPolicies() {
		if err := manager.AddPolicy(policy); err != nil {
			logging.Warnf("Failed to add default policy %q: %v", policy.ToolPattern, err)
		}
	}
	if err := manager.Save(); err != nil {
		logging.Warnf("Failed to save default policies: %v", err)
	}
}

// ApplyInlinePolicies applies per-server inline policies declared in each MCP
// server's config block, taking precedence over the default catch-all.
func ApplyInlinePolicies(cfg *config.Config, manager permissions.Manager) {
	if cfg == nil {
		return
	}
	for _, serverCfg := range cfg.MCP.Servers {
		if serverCfg.Enabled != nil && !*serverCfg.Enabled {
			continue
		}
		for _, p := range serverCfg.Policies {
			policy := permissions.PermissionPolicy{
				ToolPattern: p.ToolPattern,
				Rank:        permissions.PermissionRank(p.Rank),
				Reason:      p.Reason,
			}
			if err := manager.AddPolicy(policy); err != nil {
				logging.Warnf("Failed to apply inline policy %q for server %q: %v", p.ToolPattern, serverCfg.Name, err)
			}
		}
	}
}
