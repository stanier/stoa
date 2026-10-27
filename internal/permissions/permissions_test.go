package permissions

import (
	"context"
	"testing"
	"time"
)

func TestPermissionRankString(t *testing.T) {
	tests := []struct {
		rank     PermissionRank
		expected string
	}{
		{RankSilent, "Silent"},
		{RankMention, "Mention"},
		{RankRequest, "Request"},
		{RankAuthenticatedRequest, "Authenticated Request"},
		{RankAuthorizedRequest, "Authorized Request"},
		{RankApprovedRequest, "Approved Request"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.rank.String(); got != tt.expected {
				t.Errorf("PermissionRank.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPolicyIsExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		policy   PermissionPolicy
		expected bool
	}{
		{
			name: "no expiration",
			policy: PermissionPolicy{
				ToolPattern: "test",
				Expires:     nil,
			},
			expected: false,
		},
		{
			name: "expired",
			policy: PermissionPolicy{
				ToolPattern: "test",
				Expires:     timePtr(now.Add(-1 * time.Hour)),
			},
			expected: true,
		},
		{
			name: "not expired",
			policy: PermissionPolicy{
				ToolPattern: "test",
				Expires:     timePtr(now.Add(1 * time.Hour)),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.IsExpired(); got != tt.expected {
				t.Errorf("IsExpired() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultChecker(t *testing.T) {
	// Create a test manager
	store := NewFileStore("/tmp/test-permissions.yaml")
	manager := NewDefaultManager(store)

	// Add test policies
	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "mcp_test_read",
		Rank:        RankSilent,
		Reason:      "Test read is safe",
	})

	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "mcp_test_write",
		Rank:        RankRequest,
		Reason:      "Write requires approval",
	})

	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "mcp_*_safe_*",
		Rank:        RankMention,
		Reason:      "Safe operations are mentioned",
	})

	// Create checker
	checker := NewDefaultChecker(manager, RankRequest)

	tests := []struct {
		name           string
		toolName       string
		serverName     string
		expectedRank   PermissionRank
		expectedPrompt bool
	}{
		{
			name:           "silent tool",
			toolName:       "mcp_test_read",
			serverName:     "test",
			expectedRank:   RankSilent,
			expectedPrompt: false,
		},
		{
			name:           "request tool",
			toolName:       "mcp_test_write",
			serverName:     "test",
			expectedRank:   RankRequest,
			expectedPrompt: true,
		},
		{
			name:           "pattern match - mention",
			toolName:       "mcp_filesystem_safe_read",
			serverName:     "filesystem",
			expectedRank:   RankMention,
			expectedPrompt: false,
		},
		{
			name:           "no match - default",
			toolName:       "mcp_unknown_tool",
			serverName:     "unknown",
			expectedRank:   RankRequest, // Default
			expectedPrompt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			decision, err := checker.Check(ctx, tt.toolName, tt.serverName, nil)

			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}

			if decision.Rank != tt.expectedRank {
				t.Errorf("Check() rank = %v, want %v", decision.Rank, tt.expectedRank)
			}

			if decision.RequiresPrompt != tt.expectedPrompt {
				t.Errorf("Check() requiresPrompt = %v, want %v", decision.RequiresPrompt, tt.expectedPrompt)
			}
		})
	}
}

func TestGrantsAndRevoke(t *testing.T) {
	store := NewFileStore("/tmp/test-permissions.yaml")
	manager := NewDefaultManager(store)

	// Add a policy that requires request
	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "mcp_test_tool",
		Rank:        RankRequest,
	})

	checker := NewDefaultChecker(manager, RankRequest)
	ctx := context.Background()

	// First check - should require prompt
	decision, err := checker.Check(ctx, "mcp_test_tool", "test", nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !decision.RequiresPrompt {
		t.Error("Expected prompt required before grant")
	}

	// Grant permission
	checker.GrantPermission(PermissionGrant{
		ToolPattern: "mcp_test_tool",
		ServerName:  "test",
		Scope:       "session",
		GrantedAt:   time.Now(),
	})

	// Second check - should not require prompt (granted)
	decision, err = checker.Check(ctx, "mcp_test_tool", "test", nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if decision.RequiresPrompt {
		t.Error("Expected no prompt after grant")
	}
	if decision.Rank != RankSilent {
		t.Errorf("Expected RankSilent after grant, got %v", decision.Rank)
	}

	// Revoke permission
	checker.RevokePermission("mcp_test_tool")

	// Third check - should require prompt again
	decision, err = checker.Check(ctx, "mcp_test_tool", "test", nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !decision.RequiresPrompt {
		t.Error("Expected prompt required after revoke")
	}
}

func TestSessionGrants(t *testing.T) {
	store := NewFileStore("/tmp/test-permissions.yaml")
	manager := NewDefaultManager(store)
	checker := NewDefaultChecker(manager, RankRequest)

	// Grant session permission
	checker.GrantPermission(PermissionGrant{
		ToolPattern: "test_tool",
		Scope:       "session",
		GrantedAt:   time.Now(),
	})

	// Check that grant exists
	ctx := context.Background()
	decision, _ := checker.Check(ctx, "test_tool", "", nil)
	if decision.Rank != RankSilent {
		t.Error("Expected session grant to allow silent execution")
	}

	// Clear session grants
	checker.ClearSessionGrants()

	// Check that grant is gone
	decision, _ = checker.Check(ctx, "test_tool", "", nil)
	if decision.Rank == RankSilent {
		t.Error("Expected session grant to be cleared")
	}
}

func TestExpiredPoliciesPruned(t *testing.T) {
	now := time.Now()
	store := NewFileStore("/tmp/test-permissions-expiry.yaml")
	manager := NewDefaultManager(store)

	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "active_tool",
		Rank:        RankSilent,
	})
	manager.AddPolicy(PermissionPolicy{
		ToolPattern: "expired_tool",
		Rank:        RankSilent,
		Expires:     timePtr(now.Add(-1 * time.Hour)),
	})

	// ListPolicies must not return expired entries.
	policies := manager.ListPolicies()
	if len(policies) != 1 {
		t.Fatalf("ListPolicies() returned %d policies, want 1", len(policies))
	}
	if policies[0].ToolPattern != "active_tool" {
		t.Errorf("ListPolicies() returned unexpected policy %q", policies[0].ToolPattern)
	}

	// GetPolicy must not match an expired policy.
	_, err := manager.GetPolicy("expired_tool", "")
	if err == nil {
		t.Error("GetPolicy() should return error for expired tool, got nil")
	}

	// Save must not persist expired entries.
	if err := manager.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load() error = %v", err)
	}
	for _, p := range data.Policies {
		if p.ToolPattern == "expired_tool" {
			t.Error("Save() persisted an expired policy")
		}
	}

	// Load must drop expired entries that are on disk.
	manager2 := NewDefaultManager(store)
	// Manually inject an expired policy into the store to simulate a stale file.
	staleData := &StorageData{
		Policies: []PermissionPolicy{
			{ToolPattern: "stale_tool", Rank: RankSilent, Expires: timePtr(now.Add(-1 * time.Hour))},
			{ToolPattern: "fresh_tool", Rank: RankSilent},
		},
	}
	if err := store.Save(staleData); err != nil {
		t.Fatalf("store.Save() error = %v", err)
	}
	if err := manager2.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	loaded := manager2.ListPolicies()
	if len(loaded) != 1 || loaded[0].ToolPattern != "fresh_tool" {
		t.Errorf("Load() did not drop expired policy; got %v", loaded)
	}
}

// Helper functions

func timePtr(t time.Time) *time.Time {
	return &t
}

func TestSessionGrantsAreScopedToTheirSession(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/permissions.yaml")
	checker := NewDefaultChecker(NewDefaultManager(store), RankRequest)

	checker.GrantPermission(PermissionGrant{
		ToolPattern: "test_tool",
		Scope:       "session",
		GrantedAt:   time.Now(),
		SessionID:   "session-a",
	})

	decision, _ := checker.Check(WithSessionID(context.Background(), "session-a"), "test_tool", "", nil)
	if decision.Rank != RankSilent {
		t.Error("expected the grant to apply in the session that made it")
	}

	decision, _ = checker.Check(WithSessionID(context.Background(), "session-b"), "test_tool", "", nil)
	if !decision.RequiresPrompt {
		t.Error("a session grant from session-a must not apply in session-b")
	}

	decision, _ = checker.Check(context.Background(), "test_tool", "", nil)
	if !decision.RequiresPrompt {
		t.Error("a session grant from session-a must not apply with no session in context")
	}
}
