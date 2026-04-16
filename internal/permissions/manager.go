package permissions

import (
	"fmt"
	"path/filepath"
	"sync"
)

// DefaultManager implements Manager
type DefaultManager struct {
	store    Store
	policies []PermissionPolicy
	mu       sync.RWMutex
}

// NewDefaultManager creates a new manager with the given store
func NewDefaultManager(store Store) *DefaultManager {
	return &DefaultManager{
		store:    store,
		policies: []PermissionPolicy{},
	}
}

// AddPolicy adds or updates a policy
func (m *DefaultManager) AddPolicy(policy PermissionPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if policy with same pattern already exists
	for i, p := range m.policies {
		if p.ToolPattern == policy.ToolPattern && p.ServerName == policy.ServerName {
			// Update existing policy
			m.policies[i] = policy
			return nil
		}
	}

	// Add new policy
	m.policies = append(m.policies, policy)
	return nil
}

// RemovePolicy removes a policy by pattern
func (m *DefaultManager) RemovePolicy(pattern string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find and remove policy
	for i, p := range m.policies {
		if p.ToolPattern == pattern {
			// Remove by replacing with last element and truncating
			m.policies[i] = m.policies[len(m.policies)-1]
			m.policies = m.policies[:len(m.policies)-1]
			return nil
		}
	}

	return fmt.Errorf("policy not found: %s", pattern)
}

// ListPolicies returns all non-expired policies.
func (m *DefaultManager) ListPolicies() []PermissionPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := make([]PermissionPolicy, 0, len(m.policies))
	for _, p := range m.policies {
		if !p.IsExpired() {
			active = append(active, p)
		}
	}
	return active
}

// GetPolicy returns the effective policy for a tool
// Matches are done in order of specificity:
// 1. Exact match with server name
// 2. Pattern match with server name
// 3. Exact match without server name
// 4. Pattern match without server name
func (m *DefaultManager) GetPolicy(toolName, serverName string) (*PermissionPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var bestMatch *PermissionPolicy
	var bestMatchScore int // Higher score = more specific match

	for i := range m.policies {
		policy := &m.policies[i]

		// Skip expired policies
		if policy.IsExpired() {
			continue
		}

		// Check if server name matches (if specified)
		serverMatches := policy.ServerName == "" || policy.ServerName == serverName

		if !serverMatches {
			continue
		}

		// Check if tool pattern matches
		matched, err := filepath.Match(policy.ToolPattern, toolName)
		if err != nil {
			// Invalid pattern, skip
			continue
		}

		if !matched {
			continue
		}

		// Calculate match score
		score := 0

		// Exact tool name match is better than pattern
		if policy.ToolPattern == toolName {
			score += 10
		} else {
			score += 5 // Pattern match
		}

		// Server-specific match is better than global
		if policy.ServerName != "" {
			score += 2
		}

		// Update best match if this is better
		if bestMatch == nil || score > bestMatchScore {
			bestMatch = policy
			bestMatchScore = score
		}
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("no matching policy found for tool: %s", toolName)
	}

	return bestMatch, nil
}

// Save saves policies to disk. Expired policies are not written so the file is
// pruned on each save.
func (m *DefaultManager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := make([]PermissionPolicy, 0, len(m.policies))
	for _, p := range m.policies {
		if !p.IsExpired() {
			active = append(active, p)
		}
	}

	data := &StorageData{
		Policies: active,
		Grants:   []PermissionGrant{}, // Grants are managed separately by Checker
	}

	return m.store.Save(data)
}

// Load loads policies from disk. Expired policies are dropped so they don't
// accumulate in memory across restarts.
func (m *DefaultManager) Load() error {
	data, err := m.store.Load()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	active := make([]PermissionPolicy, 0, len(data.Policies))
	for _, p := range data.Policies {
		if !p.IsExpired() {
			active = append(active, p)
		}
	}
	m.policies = active
	return nil
}
