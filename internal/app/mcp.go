package app

import (
	"context"
	"time"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/mcp"
	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/skills"
)

// MCPSetupOptions configures the async MCP connect/discover operation.
type MCPSetupOptions struct {
	// Permission components — if PermChecker is non-nil, tools are wrapped with
	// PermissionAwareMCPTool using the other fields.
	PermChecker     permissions.Checker
	AuditLogger     *permissions.AuditLogger
	PermReqChan     chan *permissions.PermissionRequest
	PermMentionChan chan *permissions.PermissionMention

	// OnComplete is called (from the background goroutine) once all tools have
	// been registered. toolCount is 0 on error. May be nil.
	OnComplete func(toolCount int)

	// ConnectTimeout overrides the default 15-second MCP connection timeout.
	ConnectTimeout time.Duration
}

// SetupMCPManager creates the MCP server manager and starts async connection/discovery.
// Tool registration happens in the background; the manager is returned immediately.
// Returns nil if MCP is not configured.
func SetupMCPManager(cfg *config.Config, registry *skills.Registry, opts MCPSetupOptions) *mcp.ServerManager {
	if cfg == nil || !cfg.MCP.Enabled || len(cfg.MCP.Servers) == 0 {
		return nil
	}

	timeout := opts.ConnectTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	mcpManager := mcp.NewServerManager(cfg.MCP)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := mcpManager.ConnectAll(ctx); err != nil {
			logging.Warnf("MCP: some servers failed to connect (continuing with available servers): %v", err)
		}

		tools, err := mcpManager.DiscoverTools(ctx)
		if err != nil {
			logging.Errorf("MCP tool discovery error: %v", err)
			if opts.OnComplete != nil {
				opts.OnComplete(0)
			}
			return
		}

		if opts.PermChecker != nil {
			for _, tool := range tools {
				if mcpTool, ok := tool.(*mcp.MCPToolAdapter); ok {
					permTool := mcp.NewPermissionAwareMCPTool(
						mcpTool,
						opts.PermChecker,
						opts.AuditLogger,
						mcpTool.ServerName(),
						opts.PermReqChan,
						opts.PermMentionChan,
					)
					registry.RegisterTool(permTool)
				} else {
					registry.RegisterTool(tool)
				}
			}
		} else {
			for _, tool := range tools {
				registry.RegisterTool(tool)
			}
		}

		logging.Infof("Registered %d MCP tools", len(tools))
		if opts.OnComplete != nil {
			opts.OnComplete(len(tools))
		}
	}()

	return mcpManager
}
