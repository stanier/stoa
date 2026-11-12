package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPRequestBlocksPrivate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal api"))
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	s := NewServer("mcp-http", "test", Config{})
	for _, target := range []string{
		ts.URL,
		"http://0.0.0.0:" + u.Port(),
		"http://localhost:" + u.Port(),
		"http://[::1]:" + u.Port(),
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.100/",
	} {
		res, err := s.CallTool(context.Background(), "http_request", map[string]interface{}{
			"method": "GET", "url": target,
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		text := res.Content[0].Text
		if !res.IsError || strings.Contains(text, "internal api") {
			t.Errorf("%s: isErr=%v text=%q, want blocked", target, res.IsError, text)
		}
	}
}

func TestHTTPRequestValidation(t *testing.T) {
	s := NewServer("mcp-http", "test", Config{AllowedDomains: []string{"api.example.com"}})
	cases := []map[string]interface{}{
		{"method": "GET"},
		{"method": "TRACE", "url": "https://api.example.com/"},
		{"method": "GET", "url": "file:///etc/passwd"},
		{"method": "GET", "url": "https://evil.example.net/"},
	}
	for _, args := range cases {
		res, err := s.CallTool(context.Background(), "http_request", args)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Errorf("%v: want error result", args)
		}
	}
}
