package graphql

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildTransportBlocksPrivateWithoutProxy(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer ts.Close()
	c := &http.Client{Transport: buildTransport()}
	resp, err := c.Get(ts.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("request to 127.0.0.1 succeeded, want blocked")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v, want a block", err)
	}
}
