package acpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverBlocksPrivateEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"name":"internal"}]}`))
	}))
	defer ts.Close()
	s := NewServer("mcp-acp", "test", Config{})
	res, err := s.CallTool(context.Background(), "acp_discover", map[string]interface{}{"endpoint": ts.URL})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not allowed") {
		t.Fatalf("private endpoint reached: %+v", res)
	}
}
