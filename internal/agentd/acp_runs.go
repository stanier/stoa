package agentd

import (
	"context"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
)

// ACPRunStatus is the lifecycle state of an ACP run.
type ACPRunStatus string

const (
	ACPRunStatusCreated   ACPRunStatus = "created"
	ACPRunStatusRunning   ACPRunStatus = "running"
	ACPRunStatusAwaiting  ACPRunStatus = "awaiting"
	ACPRunStatusCompleted ACPRunStatus = "completed"
	ACPRunStatusFailed    ACPRunStatus = "failed"
	ACPRunStatusCancelled ACPRunStatus = "cancelled"
)

// ACPRun tracks a single ACP run's lifecycle and output.
type ACPRun struct {
	ID        string
	SessionID string
	AgentName string
	Status    ACPRunStatus
	Output    []acpMessage
	Error     string

	CreatedAt  time.Time
	FinishedAt *time.Time

	eventCh chan agentproto.Event
	cancel  context.CancelFunc

	mu sync.Mutex
}

// toResponse serialises the run to a wire-friendly struct.
func (r *ACPRun) toResponse() acpRunResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	return acpRunResponse{
		ID:         r.ID,
		AgentName:  r.AgentName,
		SessionID:  r.SessionID,
		Status:     string(r.Status),
		Output:     r.Output,
		Error:      r.Error,
		CreatedAt:  r.CreatedAt,
		FinishedAt: r.FinishedAt,
	}
}

// ---------------------------------------------------------------------------
// Wire types — these mirror what would come from agentproto once merged.
// Replace with agentproto.ACPMessage etc. when the upstream branch lands.
// ---------------------------------------------------------------------------

// acpContentPart is a single content part in an ACP message.
type acpContentPart struct {
	ContentType string `json:"content_type"` // e.g. "text/plain"
	Content     string `json:"content"`
}

// acpMessage is an ACP protocol message (user or assistant turn).
type acpMessage struct {
	Role  string           `json:"role"`
	Parts []acpContentPart `json:"parts"`
}

// acpRunRequest is the body of POST /acp/runs.
type acpRunRequest struct {
	AgentName string       `json:"agent_name"`
	Mode      string       `json:"mode"` // "sync" | "async" | "stream"
	Messages  []acpMessage `json:"messages"`
	SessionID string       `json:"session_id,omitempty"` // optional: reuse session
}

// acpRunResponse is the body returned for run status queries.
type acpRunResponse struct {
	ID         string       `json:"id"`
	AgentName  string       `json:"agent_name"`
	SessionID  string       `json:"session_id"`
	Status     string       `json:"status"`
	Output     []acpMessage `json:"output,omitempty"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
}

// acpAgentManifest is the ACP agent descriptor returned by GET /acp/agents/{name}.
type acpAgentManifest struct {
	Name               string   `json:"name"`
	DisplayName        string   `json:"display_name"`
	Description        string   `json:"description"`
	InputContentTypes  []string `json:"input_content_types"`
	OutputContentTypes []string `json:"output_content_types"`
	Visibility         string   `json:"visibility"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
}

// acpAgentsListResponse wraps a list of agent manifests.
type acpAgentsListResponse struct {
	Agents []acpAgentManifest `json:"agents"`
}

// acpResumeRequest routes a permission response into a running ACP run.
type acpResumeRequest struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}
