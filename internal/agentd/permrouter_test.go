package agentd

import (
	"testing"
	"time"

	"github.com/stanier/stoa/internal/permissions"
)

func newPermReq(sessionID string) *permissions.PermissionRequest {
	return &permissions.PermissionRequest{
		ID:           "req-" + sessionID,
		ToolName:     "shell_exec",
		SessionID:    sessionID,
		ResponseChan: make(chan permissions.PermissionResponse, 1),
	}
}

func TestPermRouter_DeliversOnlyToOwningSession(t *testing.T) {
	r := newPermRouter()
	subA, unsubA := r.subscribe("session-a")
	defer unsubA()
	subB, unsubB := r.subscribe("session-b")
	defer unsubB()

	r.routeRequest(newPermReq("session-a"))
	r.routeMention(&permissions.PermissionMention{ToolName: "read_file", SessionID: "session-a"})

	select {
	case got := <-subA.reqs:
		if got.SessionID != "session-a" {
			t.Fatalf("session A got a request for %q", got.SessionID)
		}
	default:
		t.Fatal("session A did not receive its request")
	}
	if len(subA.mentions) != 1 {
		t.Fatal("session A did not receive its mention")
	}
	if len(subB.reqs) != 0 || len(subB.mentions) != 0 {
		t.Fatal("session B received another session's permission events")
	}
}

func TestPermRouter_DeniesWhenNoClientForSession(t *testing.T) {
	r := newPermRouter()
	_, unsub := r.subscribe("someone-else")
	defer unsub()

	req := newPermReq("unattended")
	r.routeRequest(req)

	select {
	case resp := <-req.ResponseChan:
		if resp.Approved {
			t.Fatal("request with no client was approved")
		}
	default:
		t.Fatal("request with no client was neither delivered nor denied")
	}
}

func TestPermRouter_UnsubscribeDeniesQueuedRequests(t *testing.T) {
	r := newPermRouter()
	_, unsub := r.subscribe("session-a")
	req := newPermReq("session-a")
	r.routeRequest(req)
	unsub()

	select {
	case resp := <-req.ResponseChan:
		if resp.Approved {
			t.Fatal("queued request was approved on disconnect")
		}
	default:
		t.Fatal("queued request was left hanging on disconnect")
	}
	if r.target("session-a") != nil {
		t.Fatal("subscriber still registered after unsubscribe")
	}
}

func TestPermRouter_RunRoutesFromGlobalChannels(t *testing.T) {
	r := newPermRouter()
	sub, unsub := r.subscribe("session-a")
	defer unsub()

	reqs := make(chan *permissions.PermissionRequest, 1)
	mentions := make(chan *permissions.PermissionMention, 1)
	stop := make(chan struct{})
	defer close(stop)
	go r.run(reqs, mentions, stop)

	reqs <- newPermReq("session-a")
	select {
	case <-sub.reqs:
	case <-time.After(2 * time.Second):
		t.Fatal("router did not forward the request")
	}
}
