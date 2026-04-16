package permissions

import (
	"context"
	"time"
)

// PermissionRank represents the security level required for a tool
type PermissionRank int

const (
	RankSilent               PermissionRank = iota // 0 - Execute without notification
	RankMention                                     // 1 - Mention execution to user
	RankRequest                                     // 2 - Request user permission
	RankAuthenticatedRequest                        // 3 - Require authentication (not implemented)
	RankAuthorizedRequest                           // 4 - Require delegation (not implemented)
	RankApprovedRequest                             // 5 - Require approval workflow (not implemented)
)

// String returns the human-readable name of the rank
func (r PermissionRank) String() string {
	switch r {
	case RankSilent:
		return "Silent"
	case RankMention:
		return "Mention"
	case RankRequest:
		return "Request"
	case RankAuthenticatedRequest:
		return "Authenticated Request"
	case RankAuthorizedRequest:
		return "Authorized Request"
	case RankApprovedRequest:
		return "Approved Request"
	default:
		return "Unknown"
	}
}

// PermissionPolicy defines rules for tool execution
type PermissionPolicy struct {
	ToolPattern string         `yaml:"tool_pattern" json:"tool_pattern"` // Glob pattern matching tool names
	Rank        PermissionRank `yaml:"rank" json:"rank"`                 // Required permission rank
	ServerName  string         `yaml:"server_name,omitempty" json:"server_name,omitempty"`
	Reason      string         `yaml:"reason,omitempty" json:"reason,omitempty"`
	Expires     *time.Time     `yaml:"expires,omitempty" json:"expires,omitempty"`
}

// IsExpired returns true if the policy has expired
func (p *PermissionPolicy) IsExpired() bool {
	if p.Expires == nil {
		return false
	}
	return time.Now().After(*p.Expires)
}

// PermissionGrant represents a user-granted permission
type PermissionGrant struct {
	ToolPattern string    `yaml:"tool_pattern" json:"tool_pattern"`
	ServerName  string    `yaml:"server_name,omitempty" json:"server_name,omitempty"`
	Scope       string    `yaml:"scope" json:"scope"` // "session" or "always"
	GrantedAt   time.Time `yaml:"granted_at" json:"granted_at"`
}

// PermissionDecision represents the result of a permission check
type PermissionDecision struct {
	Allowed        bool
	Rank           PermissionRank
	Policy         *PermissionPolicy
	RequiresPrompt bool   // Whether to show UI prompt
	MentionText    string // Text to show user (for Rank 1+)
}

// PermissionRequest represents a pending permission request
type PermissionRequest struct {
	ID           string
	ToolName     string
	ServerName   string
	Description  string
	Parameters   map[string]interface{}
	Rank         PermissionRank
	RequestedAt  time.Time
	ResponseChan chan PermissionResponse
}

// PermissionResponse represents user's decision on a permission request
type PermissionResponse struct {
	Approved       bool
	RememberChoice bool   // "Always allow" / "Always deny"
	Scope          string // "session" or "always"
}

// AuditEntry logs permission decisions
type AuditEntry struct {
	Timestamp  time.Time              `json:"timestamp"`
	ToolName   string                 `json:"tool_name"`
	ServerName string                 `json:"server_name,omitempty"`
	Rank       PermissionRank         `json:"rank"`
	Decision   bool                   `json:"decision"` // Allowed or denied
	UserID     string                 `json:"user_id,omitempty"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// PermissionMention represents a notification that a tool was executed (Rank 1)
type PermissionMention struct {
	ToolName   string
	ServerName string
	Message    string
	Timestamp  time.Time
}

// Checker checks if a tool execution is permitted
type Checker interface {
	// Check performs a permission check for a tool execution
	Check(ctx context.Context, toolName, serverName string, params map[string]interface{}) (*PermissionDecision, error)

	// GrantPermission adds a runtime grant (for "remember choice")
	GrantPermission(grant PermissionGrant)

	// RevokePermission removes a runtime grant
	RevokePermission(toolPattern string)

	// ClearSessionGrants removes all session-scoped grants
	ClearSessionGrants()
}

// Manager manages permission policies
type Manager interface {
	// AddPolicy adds or updates a policy
	AddPolicy(policy PermissionPolicy) error

	// RemovePolicy removes a policy by pattern
	RemovePolicy(pattern string) error

	// ListPolicies returns all policies
	ListPolicies() []PermissionPolicy

	// GetPolicy returns the effective policy for a tool
	GetPolicy(toolName, serverName string) (*PermissionPolicy, error)

	// Save saves policies to disk
	Save() error

	// Load loads policies from disk
	Load() error
}

// Store persists permission policies and grants
type Store interface {
	// Save saves the storage data
	Save(data *StorageData) error

	// Load loads the storage data
	Load() (*StorageData, error)
}

// StorageData represents the persisted permission data
type StorageData struct {
	Policies []PermissionPolicy `yaml:"policies" json:"policies"`
	Grants   []PermissionGrant  `yaml:"grants" json:"grants"`
}
