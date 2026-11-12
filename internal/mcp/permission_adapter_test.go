package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stanier/stoa/internal/permissions"
)

// The request a tool raises must name the session whose tool call raised it,
// so agentd can route the prompt to that session's client only.
func TestPermissionAwareMCPTool_RequestCarriesSessionID(t *testing.T) {
	store := permissions.NewFileStore(t.TempDir() + "/permissions.yaml")
	checker := permissions.NewDefaultChecker(permissions.NewDefaultManager(store), permissions.RankRequest)
	reqs := make(chan *permissions.PermissionRequest, 1)
	tool := NewPermissionAwareMCPTool(
		NewMCPToolAdapter(nil, "shell", ToolDescription{Name: "exec"}),
		checker, nil, "shell", reqs, nil,
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := permissions.WithSessionID(context.Background(), "session-a")
		res, _ := tool.Execute(ctx, json.RawMessage(`{}`))
		if res.Success {
			t.Error("denied tool reported success")
		}
	}()

	select {
	case req := <-reqs:
		if req.SessionID != "session-a" {
			t.Fatalf("request SessionID = %q, want session-a", req.SessionID)
		}
		req.ResponseChan <- permissions.PermissionResponse{Approved: false}
	case <-time.After(2 * time.Second):
		t.Fatal("no permission request raised")
	}
	<-done
}
