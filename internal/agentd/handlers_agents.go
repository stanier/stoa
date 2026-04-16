package agentd

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/logging"
)

// handleAgents dispatches GET /agents and POST /agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListAgents(w, r)
	case http.MethodPost:
		s.handleCreateAgent(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAgentsByName dispatches GET/PUT/DELETE/POST /agents/{name}[/action].
func (s *Server) handleAgentsByName(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/agents/")
	// Check for sub-action e.g. /agents/{name}/publish
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	if name == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "publish":
			s.handlePublishAgent(w, r, name)
		case "unpublish":
			s.handleUnpublishAgent(w, r, name)
		default:
			http.NotFound(w, r)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetAgent(w, r, name)
	case http.MethodPut:
		s.handleUpdateAgent(w, r, name)
	case http.MethodDelete:
		s.handleDeleteAgent(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListAgents handles GET /agents.
// Authenticated callers see internal + published external agents.
// Anonymous callers see only published external agents.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	_, isAuthed := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	isAuthed = isAuthed || isService

	var agents []agentdb.Agent
	var err error
	if isAuthed {
		// Return all internal agents plus all published external agents.
		internalAgents, iErr := s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityInternal), false)
		externalAgents, eErr := s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityExternal), true)
		if iErr != nil {
			logging.Warnf("agentd: list internal agents: %v", iErr)
		}
		if eErr != nil {
			logging.Warnf("agentd: list external agents: %v", eErr)
		}
		err = iErr
		if err == nil {
			err = eErr
		}
		agents = append(internalAgents, externalAgents...)
	} else {
		// Anonymous: only published external agents.
		agents, err = s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityExternal), true)
	}

	if err != nil {
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}
	if agents == nil {
		agents = []agentdb.Agent{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agents)
}

// handleCreateAgent handles POST /agents.
func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	_, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var a agentdb.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.agentDB.CreateAgent(r.Context(), a); err != nil {
		logging.Warnf("agentd: create agent %q: %v", a.Name, err)
		http.Error(w, "failed to create agent", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a)
}

// handleGetAgent handles GET /agents/{name}.
func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request, name string) {
	a, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

// handleUpdateAgent handles PUT /agents/{name}.
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request, name string) {
	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	existing, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	// Ownership check for non-service users.
	if !isService && existing.OwnerUserID != "" && userID != existing.OwnerUserID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var a agentdb.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.agentDB.UpdateAgent(r.Context(), name, a); err != nil {
		logging.Warnf("agentd: update agent %q: %v", name, err)
		http.Error(w, "failed to update agent", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteAgent handles DELETE /agents/{name}.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request, name string) {
	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	existing, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if !isService && existing.OwnerUserID != "" && userID != existing.OwnerUserID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.agentDB.DeleteAgent(r.Context(), name); err != nil {
		logging.Warnf("agentd: delete agent %q: %v", name, err)
		http.Error(w, "failed to delete agent", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePublishAgent handles POST /agents/{name}/publish.
func (s *Server) handlePublishAgent(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	existing, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if !isService && existing.OwnerUserID != "" && userID != existing.OwnerUserID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.agentDB.PublishAgent(r.Context(), name); err != nil {
		logging.Warnf("agentd: publish agent %q: %v", name, err)
		http.Error(w, "failed to publish agent", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnpublishAgent handles POST /agents/{name}/unpublish.
func (s *Server) handleUnpublishAgent(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	existing, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if !isService && existing.OwnerUserID != "" && userID != existing.OwnerUserID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.agentDB.UnpublishAgent(r.Context(), name); err != nil {
		logging.Warnf("agentd: unpublish agent %q: %v", name, err)
		http.Error(w, "failed to unpublish agent", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// buildLLMContextFromDBAgent constructs an LLMContext from a published DB agent,
// using the agent's system_prompt and allowed_tools directly.
func buildLLMContextFromDBAgent(s *Server, a *agentdb.Agent) app.LLMContext {
	return app.BuildLLMContext(
		s.cfg,
		s.skillRegistry,
		s.promptReg,
		s.personaReg,
		a.Name,
		a.AllowedTools,
		s.agentSkills...,
	)
}
