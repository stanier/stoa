package webd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRealtimeProxy(t *testing.T) {
	var gotAuth, gotQuery string
	up := websocket.Upgrader{}
	agentd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime" {
			http.NotFound(w, r)
			return
		}
		gotAuth, gotQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"ready","input_rate":16000}`))
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.WriteMessage(websocket.TextMessage, append([]byte("echo:"), data...))
		}
	}))
	defer agentd.Close()

	s, err := NewServer(&Config{AgentdURL: agentd.URL, ExternalURL: "https://stoa.example"})
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(s.Router())
	defer web.Close()
	wsURL := "ws" + strings.TrimPrefix(web.URL, "http") + "/api/realtime?provider=llama&model=omni&extra=dropped"

	// No session cookie: refused before any upgrade.
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dial: err=%v resp=%v", err, resp)
	}

	// Wrong origin: refused by the upgrader.
	hdr := http.Header{}
	hdr.Set("Cookie", sessionCookie+"=tok123")
	hdr.Set("Origin", "https://evil.example")
	if _, _, err := websocket.DefaultDialer.Dial(wsURL, hdr); err == nil {
		t.Fatal("cross-origin upgrade should be refused")
	}

	hdr.Set("Origin", "https://stoa.example")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, first, err := c.ReadMessage()
	if err != nil || !strings.Contains(string(first), `"ready"`) {
		t.Fatalf("first frame %q err=%v", first, err)
	}
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"turn.end"}`))
	_, echo, err := c.ReadMessage()
	if err != nil || string(echo) != `echo:{"type":"turn.end"}` {
		t.Fatalf("echo %q err=%v", echo, err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("agentd auth = %q", gotAuth)
	}
	if gotQuery != "model=omni&provider=llama" {
		t.Errorf("agentd query = %q (extra params must be dropped)", gotQuery)
	}
}
