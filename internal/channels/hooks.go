package channels

import "time"

// HookEvent carries contextual data passed to every hook callback.
// Fields are populated on a best-effort basis; callers should nil-check
// optional pointer fields before dereferencing.
type HookEvent struct {
	// ChannelName is the originating channel (e.g. "slack", "discord", "zulip", "matrix").
	ChannelName string

	// SessionKey is the bridge-internal conversation key (e.g. "dm:U123", "channel:C456").
	SessionKey string

	// SessionID is the agentd session UUID, if a session has been established.
	SessionID string

	// Msg is the channel message that triggered the event, if applicable.
	Msg *Message

	// ToolName is populated for tool lifecycle events.
	ToolName string

	// PermRequestID is populated for permission request/response events.
	PermRequestID string

	// Approved is set for permission response events.
	Approved bool

	// Err holds any error associated with the event.
	Err error

	// At is the wall-clock time the event occurred.
	At time.Time
}

// Hooks is the set of optional callbacks that a bridge implementation may
// register to observe lifecycle events.  All fields are optional; nil hooks
// are silently skipped.
//
// Hooks are called synchronously from the goroutine that detected the event,
// so implementations must not block.  Use a buffered channel or spawn a
// goroutine inside the hook if you need to do I/O.
type Hooks struct {
	// OnMessageReceived is called after a message is pulled from the channel
	// and before it is dispatched to the agent session.
	OnMessageReceived func(e HookEvent)

	// OnResponseSent is called after the agent's final response has been
	// delivered back to the channel.
	OnResponseSent func(e HookEvent)

	// OnToolInvoked is called when agentd emits an EventTypeToolInvoked event.
	OnToolInvoked func(e HookEvent)

	// OnPermRequest is called when agentd raises a permission-request event
	// and the bridge is about to prompt the user for approval.
	OnPermRequest func(e HookEvent)

	// OnPermResponse is called once the user has responded to a permission
	// request (or the request timed out).  Approved is true when the user
	// approved the action.
	OnPermResponse func(e HookEvent)

	// OnSessionCreated is called immediately after a new agentd session has
	// been created for a conversation key.
	OnSessionCreated func(e HookEvent)

	// OnSessionEvicted is called when an idle session is removed from the
	// bridge's session map.
	OnSessionEvicted func(e HookEvent)

	// OnError is called whenever a non-fatal error occurs inside the bridge
	// (e.g. a failed SendResponse, a failed AddReaction, etc.).
	OnError func(e HookEvent)
}

// newHookEvent returns a HookEvent pre-populated with the current time and
// the supplied channel name.  Callers should set additional fields as needed.
func newHookEvent(channelName string) HookEvent {
	return HookEvent{
		ChannelName: channelName,
		At:          time.Now(),
	}
}

// fire calls fn(e) if fn is not nil.
func fire(fn func(HookEvent), e HookEvent) {
	if fn != nil {
		fn(e)
	}
}

// Fire calls fn(e) if h is non-nil and fn is non-nil.
func (h *Hooks) Fire(fn func(HookEvent), e HookEvent) {
	if h != nil {
		fire(fn, e)
	}
}

// NewHookEvent is the exported constructor for use by bridge packages.
// It returns a HookEvent pre-populated with the current time.
func NewHookEvent(channelName string) HookEvent {
	return newHookEvent(channelName)
}
