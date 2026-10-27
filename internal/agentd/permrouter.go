package agentd

import (
	"sync"

	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/permissions"
)

// permSubBuffer is the per-connection buffer for routed permission events.
const permSubBuffer = 10

// permRouter delivers permission requests and mentions to the connection that
// serves the session which raised them.
//
// MCP tools are registered once in the shared skill registry, so they all send
// to the same global channels (app.PermissionsSetup). Each request carries the
// session ID taken from the tool call's context (permissions.WithSessionID);
// the router reads the global channels and hands each event to that session's
// subscriber only. A request for a session with no connected client is denied
// rather than shown to someone else.
type permRouter struct {
	mu   sync.Mutex
	subs map[string][]*permSub // session ID → subscribers, newest last
}

// permSub is one client connection's view of its session's permission events.
type permSub struct {
	reqs     chan *permissions.PermissionRequest
	mentions chan *permissions.PermissionMention
}

func newPermRouter() *permRouter {
	return &permRouter{subs: make(map[string][]*permSub)}
}

// subscribe registers a connection for sessionID's permission events. When a
// session has several connections, the most recent one receives them. The
// returned func unregisters the connection and denies anything still queued.
func (r *permRouter) subscribe(sessionID string) (*permSub, func()) {
	sub := &permSub{
		reqs:     make(chan *permissions.PermissionRequest, permSubBuffer),
		mentions: make(chan *permissions.PermissionMention, permSubBuffer),
	}
	r.mu.Lock()
	r.subs[sessionID] = append(r.subs[sessionID], sub)
	r.mu.Unlock()

	return sub, func() {
		r.mu.Lock()
		list := r.subs[sessionID]
		for i, s := range list {
			if s == sub {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(r.subs, sessionID)
		} else {
			r.subs[sessionID] = list
		}
		r.mu.Unlock()

		for {
			select {
			case req := <-sub.reqs:
				denyPermission(req)
			default:
				return
			}
		}
	}
}

// target returns the subscriber that should receive sessionID's events, or nil.
func (r *permRouter) target(sessionID string) *permSub {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.subs[sessionID]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

// routeRequest delivers req to its session's connection, or denies it.
func (r *permRouter) routeRequest(req *permissions.PermissionRequest) {
	sub := r.target(req.SessionID)
	if sub == nil {
		logging.Warnf("agentd: permission request for %q has no client for session %q; denying", req.ToolName, req.SessionID)
		denyPermission(req)
		return
	}
	select {
	case sub.reqs <- req:
	default:
		logging.Warnf("agentd: permission queue full for session %s; denying %q", req.SessionID, req.ToolName)
		denyPermission(req)
	}
}

// routeMention delivers m to its session's connection; mentions are FYI only
// and are dropped when nobody is listening.
func (r *permRouter) routeMention(m *permissions.PermissionMention) {
	sub := r.target(m.SessionID)
	if sub == nil {
		return
	}
	select {
	case sub.mentions <- m:
	default:
	}
}

// run drains the global permission channels until stop is closed.
func (r *permRouter) run(reqs <-chan *permissions.PermissionRequest, mentions <-chan *permissions.PermissionMention, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case req, ok := <-reqs:
			if !ok {
				reqs = nil
				continue
			}
			r.routeRequest(req)
		case m, ok := <-mentions:
			if !ok {
				mentions = nil
				continue
			}
			r.routeMention(m)
		}
	}
}

func denyPermission(req *permissions.PermissionRequest) {
	select {
	case req.ResponseChan <- permissions.PermissionResponse{Approved: false}:
	default:
	}
}
