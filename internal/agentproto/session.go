package agentproto

import "time"

// CurrentProtocolVersion is the agentproto WebSocket protocol version
// implemented by this build. Clients should send this in SessionConfig so the
// server can detect incompatible clients.
const CurrentProtocolVersion = "1"

// SessionConfig is passed to CreateSession.
type SessionConfig struct {
	// Persona selects the system prompt persona (e.g. "coder", "analyzer").
	// Empty string uses the default persona.
	Persona string `json:"persona,omitempty"`
	// ProtocolVersion is the agentproto version the client implements.
	// Servers may reject or warn on mismatches. Omit to skip version checking.
	ProtocolVersion string `json:"protocol_version,omitempty"`
	// Source identifies the client that created the session.
	// Convention: "web", "tui", "zulip", "cli". Empty means unknown.
	Source string `json:"source,omitempty"`
	// UserID is the authd user UUID of the authenticated user creating the session.
	// Empty for service accounts and anonymous sessions.
	UserID string `json:"user_id,omitempty"`
	// AllowedTools is an optional list of glob patterns restricting which tools
	// are available in this session. Empty means the persona's full tool set.
	AllowedTools []string `json:"allowed_tools,omitempty"`
}

// SessionInfo describes an active session.
type SessionInfo struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id,omitempty"`
	Persona      string    `json:"persona"`
	Source       string    `json:"source"`
	Title        string    `json:"title,omitempty"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at,omitempty"`
}

// CreateSessionResponse is returned by POST /sessions.
type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
}

// CompactResponse is returned by POST /sessions/:id/compact.
type CompactResponse struct {
	MessagesBefore int `json:"messages_before"`
	MessagesAfter  int `json:"messages_after"`
	TokensBefore   int `json:"tokens_before"`
	TokensAfter    int `json:"tokens_after"`
}

// TokensResponse is returned by GET /sessions/:id/tokens.
type TokensResponse struct {
	TotalTokens  int            `json:"total_tokens"`
	TotalMessages int           `json:"total_messages"`
	ByRole       map[string]int `json:"by_role,omitempty"`
}
