package permissions

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
)

// DefaultChecker implements Checker
type DefaultChecker struct {
	manager       Manager
	grants        map[string]*PermissionGrant // Key: toolPattern
	sessionGrants map[string]*PermissionGrant // Session-only grants
	mu            sync.RWMutex
	defaultRank   PermissionRank // Default rank when no policy matches
}

// NewDefaultChecker creates a new permission checker
func NewDefaultChecker(manager Manager, defaultRank PermissionRank) *DefaultChecker {
	return &DefaultChecker{
		manager:       manager,
		grants:        make(map[string]*PermissionGrant),
		sessionGrants: make(map[string]*PermissionGrant),
		defaultRank:   defaultRank,
	}
}

// Check performs a permission check for a tool execution
func (c *DefaultChecker) Check(ctx context.Context, toolName, serverName string, params map[string]interface{}) (*PermissionDecision, error) {
	// 1. Check runtime grants first (user said "always allow" or "allow for session")
	if grant := c.getGrant(toolName, serverName); grant != nil {
		return &PermissionDecision{
			Allowed:        true,
			Rank:           RankSilent, // Already approved, treat as silent
			RequiresPrompt: false,
		}, nil
	}

	// 2. Get matching policy
	policy, err := c.manager.GetPolicy(toolName, serverName)
	if err != nil {
		// No matching policy found, use default
		policy = &PermissionPolicy{
			ToolPattern: toolName,
			Rank:        c.defaultRank,
			Reason:      "No specific policy defined, using default",
		}
	}

	// 3. Build decision based on rank
	decision := &PermissionDecision{
		Rank:   policy.Rank,
		Policy: policy,
	}

	switch policy.Rank {
	case RankSilent:
		decision.Allowed = true
		decision.RequiresPrompt = false

	case RankMention:
		decision.Allowed = true
		decision.RequiresPrompt = false
		decision.MentionText = fmt.Sprintf("Executing: %s", toolName)
		if policy.Reason != "" {
			decision.MentionText += fmt.Sprintf(" (%s)", policy.Reason)
		}

	case RankRequest:
		decision.Allowed = false // Pending approval
		decision.RequiresPrompt = true

	case RankAuthenticatedRequest, RankAuthorizedRequest, RankApprovedRequest:
		// Not implemented yet
		return nil, fmt.Errorf("permission rank %d (%s) requires authentication/authorization (not implemented)", policy.Rank, policy.Rank.String())
	}

	return decision, nil
}

// GrantPermission adds a runtime grant (for "remember choice")
func (c *DefaultChecker) GrantPermission(grant PermissionGrant) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.grantKey(grant.ToolPattern, grant.ServerName)

	if grant.Scope == "session" {
		c.sessionGrants[key] = &grant
	} else {
		c.grants[key] = &grant
	}
}

// RevokePermission removes a runtime grant
func (c *DefaultChecker) RevokePermission(toolPattern string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove from both maps (we don't know which one it's in)
	for key := range c.grants {
		if g := c.grants[key]; g != nil && g.ToolPattern == toolPattern {
			delete(c.grants, key)
		}
	}
	for key := range c.sessionGrants {
		if g := c.sessionGrants[key]; g != nil && g.ToolPattern == toolPattern {
			delete(c.sessionGrants, key)
		}
	}
}

// ClearSessionGrants removes all session-scoped grants
func (c *DefaultChecker) ClearSessionGrants() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionGrants = make(map[string]*PermissionGrant)
}

// getGrant checks if there's a grant matching the tool
func (c *DefaultChecker) getGrant(toolName, serverName string) *PermissionGrant {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check both permanent and session grants
	allGrants := make(map[string]*PermissionGrant)
	for k, v := range c.grants {
		allGrants[k] = v
	}
	for k, v := range c.sessionGrants {
		allGrants[k] = v
	}

	// Find matching grant (with server specificity)
	for _, grant := range allGrants {
		// Check server match
		serverMatches := grant.ServerName == "" || grant.ServerName == serverName
		if !serverMatches {
			continue
		}

		// Check pattern match
		matched, err := filepath.Match(grant.ToolPattern, toolName)
		if err != nil {
			continue
		}

		if matched {
			return grant
		}
	}

	return nil
}

// grantKey creates a unique key for a grant
func (c *DefaultChecker) grantKey(toolPattern, serverName string) string {
	if serverName == "" {
		return toolPattern
	}
	return fmt.Sprintf("%s:%s", serverName, toolPattern)
}

// LoadGrants loads permanent grants from storage
func (c *DefaultChecker) LoadGrants(store Store) error {
	data, err := store.Load()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Load permanent grants
	for _, grant := range data.Grants {
		if grant.Scope == "always" {
			key := c.grantKey(grant.ToolPattern, grant.ServerName)
			c.grants[key] = &grant
		}
	}

	return nil
}

// SaveGrants saves permanent grants to storage
func (c *DefaultChecker) SaveGrants(store Store) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Collect permanent grants
	var grants []PermissionGrant
	for _, grant := range c.grants {
		if grant.Scope == "always" {
			grants = append(grants, *grant)
		}
	}

	// Load existing data
	data, err := store.Load()
	if err != nil {
		return err
	}

	// Update grants
	data.Grants = grants

	return store.Save(data)
}
