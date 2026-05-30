package agentd

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
)

// handleBranchSession handles POST /sessions/:id/branch.
// It snapshots the parent session's conversation history (up to an optional
// branch_point), creates a new session with an optional new persona/model, and
// returns the new session ID.
func (s *Server) handleBranchSession(w http.ResponseWriter, r *http.Request, parent *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req agentproto.BranchSessionConfig
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	parent.mu.Lock()
	// Snapshot the message history.
	msgs := make([]chat.Message, len(parent.messages))
	copy(msgs, parent.messages)
	parentPersona := parent.persona
	parentUserID := parent.userID
	parentSource := parent.source
	totalNonSys := nonSystemCount(parent.messages)
	parent.mu.Unlock()

	branchPoint := totalNonSys
	if req.BranchPoint > 0 && req.BranchPoint < totalNonSys {
		branchPoint = req.BranchPoint
		// Trim msgs to the branch point: keep system message + first branchPoint non-system messages.
		var trimmed []chat.Message
		nonSys := 0
		for _, m := range msgs {
			trimmed = append(trimmed, m)
			if m.Role != "system" {
				nonSys++
				if nonSys >= branchPoint {
					break
				}
			}
		}
		msgs = trimmed
	}

	persona := req.Persona
	if persona == "" {
		persona = parentPersona
	}

	newID := uuid.New().String()
	newSess := newSession(newID, persona, parentSource, parentUserID, "", nil)
	newSess.tracer = app.SetupTracer(s.cfg, newID)
	newSess.llmCtx = app.BuildLLMContext(
		s.cfg,
		s.skillRegistry,
		s.promptReg,
		s.personaReg,
		persona,
		nil,
		s.agentSkills...,
	)
	newSess.perSessionTools["list_tools"] = newListToolsTool(&newSess.mu, &newSess.llmCtx)

	// New system prompt first, then replay non-system messages from parent.
	if newSess.llmCtx.SystemPrompt != "" {
		newSess.messages = append(newSess.messages, systemMessage(newSess.llmCtx.SystemPrompt))
	}
	for _, m := range msgs {
		if m.Role != "system" {
			newSess.messages = append(newSess.messages, m)
		}
	}

	// Apply model override.
	if req.Model != "" {
		newSess.overrides = &llm.Overrides{Model: &req.Model}
	}

	s.mu.Lock()
	s.sessions[newID] = newSess
	s.mu.Unlock()

	// Persist to DB if configured.
	if s.agentDB != nil {
		if err := s.agentDB.BranchSession(r.Context(), newID, parent.id, parentUserID, persona, parentSource, branchPoint); err != nil {
			logging.Warnf("agentd: db: branch session %s → %s: %v", parent.id, newID, err)
		}
		s.persistSession(newSess)
	}

	s.metrics.sessionsCreated.WithLabelValues(parentSource, persona).Inc()
	s.metrics.activeSessions.WithLabelValues(parentSource).Inc()
	logging.Infof("agentd: branched session %s → %s (persona=%q, branchPoint=%d)", parent.id, newID, persona, branchPoint)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(agentproto.BranchSessionResponse{
		SessionID:   newID,
		BranchPoint: branchPoint,
	})
}
