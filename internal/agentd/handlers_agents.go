package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

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
	prepareNewAgent(r.Context(), &a)

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
	_, ok := UserIDFromContext(r.Context())
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
	// Ownership check: only the owner (or a service account) may modify an
	// agent. Unowned agents, such as those seeded from config personas, are
	// service-only.
	if !callerMayAccess(r.Context(), existing.OwnerUserID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var a agentdb.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	prepareAgentUpdate(r.Context(), existing, &a)

	if err := s.agentDB.UpdateAgent(r.Context(), name, a); err != nil {
		logging.Warnf("agentd: update agent %q: %v", name, err)
		http.Error(w, "failed to update agent", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteAgent handles DELETE /agents/{name}.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request, name string) {
	_, ok := UserIDFromContext(r.Context())
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
	if !callerMayAccess(r.Context(), existing.OwnerUserID) {
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

	_, ok := UserIDFromContext(r.Context())
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
	if !callerMayAccess(r.Context(), existing.OwnerUserID) {
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

	_, ok := UserIDFromContext(r.Context())
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
	if !callerMayAccess(r.Context(), existing.OwnerUserID) {
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

// prepareNewAgent fixes the server-controlled fields of an agent a caller asked
// to create. A non-service caller always owns what it creates and cannot pick
// the record ID; service accounts may set both (e.g. provisioning on a user's
// behalf).
func prepareNewAgent(ctx context.Context, a *agentdb.Agent) {
	if !IsServiceFromContext(ctx) {
		if callerID, ok := UserIDFromContext(ctx); ok {
			a.OwnerUserID = callerID
		}
		a.ID = ""
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
}

// prepareAgentUpdate keeps a non-service caller from reassigning an agent's
// owner through PUT /agents/{name}.
func prepareAgentUpdate(ctx context.Context, existing *agentdb.Agent, a *agentdb.Agent) {
	if !IsServiceFromContext(ctx) {
		a.OwnerUserID = existing.OwnerUserID
	}
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
