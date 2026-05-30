package agentd

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/agentproto"
)

// handleSubAgentsRoute dispatches GET/POST /sessions/:id/subagents
// and DELETE /sessions/:id/subagents/:agentID.
func (s *Server) handleSubAgentsRoute(w http.ResponseWriter, r *http.Request, sess *Session, rest string) {
	// rest is "" for /subagents, or "<agentID>" for /subagents/<agentID>
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleListSubAgents(w, r, sess)
		case http.MethodPost:
			s.handleSpawnSubAgent(w, r, sess)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	agentID := rest
	if strings.Contains(agentID, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.handleKillSubAgent(w, r, sess, agentID)
}

func (s *Server) handleListSubAgents(w http.ResponseWriter, r *http.Request, sess *Session) {
	sess.mu.Lock()
	result := make([]agentproto.SubAgentInfo, 0, len(sess.subAgents))
	for _, sa := range sess.subAgents {
		result = append(result, agentproto.SubAgentInfo{
			ID:      sa.id,
			Persona: sa.persona,
			Task:    sa.task,
			Status:  string(sa.status),
		})
	}
	sess.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSpawnSubAgent(w http.ResponseWriter, r *http.Request, sess *Session) {
	var cfg agentproto.SpawnSubAgentConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.Task == "" {
		http.Error(w, "task is required", http.StatusBadRequest)
		return
	}
	if cfg.Persona == "" {
		cfg.Persona = "default"
	}

	agentID := spawnSubAgentInSession(s, sess, cfg.Persona, cfg.Task)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"agent_id": agentID})
}

func (s *Server) handleKillSubAgent(w http.ResponseWriter, r *http.Request, sess *Session, agentID string) {
	if !cancelSubAgentInSession(sess, agentID) {
		http.Error(w, "sub-agent not found or not active", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
