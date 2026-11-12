package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func call(t *testing.T, s *Server, tool, rawURL string) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), tool, map[string]interface{}{"url": rawURL})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res.Content[0].Text, res.IsError
}

func newPage(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>secret page</p><a href="/x">x</a></body></html>`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// The test server listens on 127.0.0.1, so it stands in for anything on the
// host or the container network that a prompt might point fetch at.
func TestFetchBlocksLoopback(t *testing.T) {
	ts := newPage(t)
	s := NewServer("mcp-fetch", "test", Config{})
	for _, tool := range []string{"fetch_url", "extract_links"} {
		text, isErr := call(t, s, tool, ts.URL)
		if !isErr || !strings.Contains(text, "not allowed") {
			t.Errorf("%s to %s: isErr=%v text=%q, want blocked", tool, ts.URL, isErr, text)
		}
	}
	// Same server through 0.0.0.0, which reaches localhost on Linux.
	u, _ := url.Parse(ts.URL)
	zero := "http://0.0.0.0:" + u.Port()
	if text, isErr := call(t, s, "fetch_url", zero); !isErr || !strings.Contains(text, "not allowed") {
		t.Errorf("fetch %s: isErr=%v text=%q, want blocked", zero, isErr, text)
	}
	if text, isErr := call(t, s, "fetch_url", "http://localhost:"+u.Port()); !isErr || !strings.Contains(text, "not allowed") {
		t.Errorf("fetch localhost: isErr=%v text=%q, want blocked", isErr, text)
	}
}

// A public page that redirects to an internal address must not be followed:
// the dial for the redirect target is checked too.
func TestFetchBlocksRedirectToPrivate(t *testing.T) {
	inner := newPage(t)
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL, http.StatusFound)
	}))
	defer outer.Close()
	u, _ := url.Parse(outer.URL)
	// Only the redirecting host is trusted.
	s := NewServer("mcp-fetch", "test", Config{AllowedHosts: []string{"localhost"}})
	text, isErr := call(t, s, "fetch_url", "http://localhost:"+u.Port())
	if !isErr || strings.Contains(text, "secret page") {
		t.Fatalf("redirect to %s was followed: isErr=%v text=%q", inner.URL, isErr, text)
	}
}

func TestFetchAllowedHostsBypass(t *testing.T) {
	ts := newPage(t)
	s := NewServer("mcp-fetch", "test", Config{AllowedHosts: []string{"127.0.0.1"}})
	text, isErr := call(t, s, "fetch_url", ts.URL)
	if isErr || !strings.Contains(text, "secret page") {
		t.Fatalf("allowed host: isErr=%v text=%q", isErr, text)
	}
}

func TestCheckURL(t *testing.T) {
	s := NewServer("mcp-fetch", "test", Config{AllowedDomains: []string{"example.com"}})
	ok := []string{"https://example.com/a", "http://docs.example.com/"}
	bad := []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/",
		"https://example.com.evil.net/",
		"https://notexample.com/",
		"://broken",
	}
	for _, u := range ok {
		if err := s.checkURL(u); err != nil {
			t.Errorf("checkURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range bad {
		if err := s.checkURL(u); err == nil {
			t.Errorf("checkURL(%q) = nil, want error", u)
		}
	}
}

func TestFetchMissingURL(t *testing.T) {
	s := NewServer("mcp-fetch", "test", Config{})
	if _, isErr := call(t, s, "fetch_url", ""); !isErr {
		t.Fatal("empty url should be an error")
	}
	res, _ := s.CallTool(context.Background(), "nope", nil)
	if !res.IsError {
		t.Fatal("unknown tool should be an error")
	}
}
