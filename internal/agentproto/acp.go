package agentproto

import (
	"encoding/json"
	"time"
)

// ACPMessage is an ACP protocol message (input or output).
type ACPMessage struct {
	Role  string    `json:"role"`
	Parts []ACPPart `json:"parts"`
}

// ACPPart is one part of an ACPMessage.
type ACPPart struct {
	ContentType string          `json:"content_type"`
	Content     string          `json:"content,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// ACPRunRequest is the body of POST /acp/runs.
type ACPRunRequest struct {
	AgentName string       `json:"agent"`
	SessionID string       `json:"session_id,omitempty"`
	Mode      string       `json:"mode"` // "sync" | "async" | "stream"
	Messages  []ACPMessage `json:"messages"`
}

// ACPRunResponse is returned by run endpoints.
type ACPRunResponse struct {
	ID         string       `json:"id"`
	AgentName  string       `json:"agent"`
	SessionID  string       `json:"session_id"`
	Status     string       `json:"status"`
	Output     []ACPMessage `json:"output,omitempty"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
}

// ACPAgentManifest is the ACP agent discovery response.
type ACPAgentManifest struct {
	Name               string          `json:"name"`
	DisplayName        string          `json:"display_name"`
	Description        string          `json:"description"`
	Visibility         string          `json:"visibility"`
	InputContentTypes  []string        `json:"input_content_types"`
	OutputContentTypes []string        `json:"output_content_types"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	PublishedAt        *time.Time      `json:"published_at,omitempty"`
}

// ACPResumeRequest is the body of POST /acp/runs/{id} (resume awaiting run).
type ACPResumeRequest struct {
	Type      string `json:"type"`        // "perm_response"
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}

// ACPAgentsListResponse is returned by GET /acp/agents.
type ACPAgentsListResponse struct {
	Agents []ACPAgentManifest `json:"agents"`
	Total  int                `json:"total"`
}
