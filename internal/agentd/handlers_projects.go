package agentd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/logging"
)

// handleProjects dispatches GET /projects and POST /projects.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListProjects(w, r)
	case http.MethodPost:
		s.handleCreateProject(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProjectsByID dispatches GET/PATCH/DELETE /projects/:id.
func (s *Server) handleProjectsByID(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/projects/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetProject(w, r, id)
	case http.MethodPatch:
		s.handlePatchProject(w, r, id)
	case http.MethodDelete:
		s.handleDeleteProject(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListProjects handles GET /projects.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	userID, _ := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	filterUserID := userID
	if isService {
		filterUserID = ""
	}

	dbProjects, err := s.agentDB.ListProjects(r.Context(), filterUserID)
	if err != nil {
		logging.Warnf("agentd: list projects: %v", err)
		http.Error(w, "failed to list projects", http.StatusInternalServerError)
		return
	}

	out := make([]agentproto.Project, 0, len(dbProjects))
	for _, p := range dbProjects {
		out = append(out, dbProjectToWire(p))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleCreateProject handles POST /projects.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req agentproto.Project
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		req.ID = "proj_" + uuid.New().String()[:6]
	}
	if req.DefaultTools == nil {
		req.DefaultTools = []string{}
	}
	if req.Files == nil {
		req.Files = []agentproto.ProjectFile{}
	}

	p := agentdb.Project{
		ID:           req.ID,
		UserID:       userID,
		Name:         req.Name,
		Description:  req.Description,
		Color:        req.Color,
		Glyph:        req.Glyph,
		Emoji:        req.Emoji,
		Instructions: req.Instructions,
		DefaultModel: req.DefaultModel,
		DefaultTools: req.DefaultTools,
		Files:        wireFilesToDB(req.Files),
	}

	if err := s.agentDB.CreateProject(r.Context(), p); err != nil {
		logging.Warnf("agentd: create project %q: %v", req.ID, err)
		http.Error(w, "failed to create project", http.StatusInternalServerError)
		return
	}

	// Re-fetch so we get the server-generated timestamps.
	created, err := s.agentDB.GetProject(r.Context(), p.ID, "")
	if err != nil || created == nil {
		// Fall back to returning the input with approximate timestamps.
		now := time.Now().UTC()
		req.CreatedAt = now
		req.UpdatedAt = now
		req.UserID = userID
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(req)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(dbProjectToWire(*created))
}

// handleGetProject handles GET /projects/:id.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request, id string) {
	userID, _ := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	filterUserID := userID
	if isService {
		filterUserID = ""
	}

	p, err := s.agentDB.GetProject(r.Context(), id, filterUserID)
	if err != nil {
		logging.Warnf("agentd: get project %q: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbProjectToWire(*p))
}

// handlePatchProject handles PATCH /projects/:id.
func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request, id string) {
	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	filterUserID := userID
	if isService {
		filterUserID = ""
	}

	existing, err := s.agentDB.GetProject(r.Context(), id, filterUserID)
	if err != nil {
		logging.Warnf("agentd: patch project get %q: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	var req agentproto.PatchProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply patch fields.
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Color != nil {
		existing.Color = *req.Color
	}
	if req.Glyph != nil {
		existing.Glyph = *req.Glyph
	}
	if req.Emoji != nil {
		existing.Emoji = *req.Emoji
	}
	if req.Instructions != nil {
		existing.Instructions = *req.Instructions
	}
	if req.DefaultModel != nil {
		existing.DefaultModel = *req.DefaultModel
	}
	if req.DefaultTools != nil {
		existing.DefaultTools = req.DefaultTools
	}
	if req.Files != nil {
		existing.Files = wireFilesToDB(req.Files)
	}

	// UserID field on existing is used for the ownership WHERE clause.
	existing.UserID = filterUserID

	if err := s.agentDB.UpdateProject(r.Context(), *existing); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		logging.Warnf("agentd: update project %q: %v", id, err)
		http.Error(w, "failed to update project", http.StatusInternalServerError)
		return
	}

	// Re-fetch to get updated_at from DB.
	updated, err := s.agentDB.GetProject(r.Context(), id, "")
	if err != nil || updated == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbProjectToWire(*updated))
}

// handleDeleteProject handles DELETE /projects/:id.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, id string) {
	userID, ok := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	if !ok && !isService {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	filterUserID := userID
	if isService {
		filterUserID = ""
	}

	if err := s.agentDB.DeleteProject(r.Context(), id, filterUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		logging.Warnf("agentd: delete project %q: %v", id, err)
		http.Error(w, "failed to delete project", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchSession handles PATCH /sessions/:id — moves a session to/from a project.
func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req agentproto.PatchSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ProjectID == nil {
		http.Error(w, "bad request: project_id field required", http.StatusBadRequest)
		return
	}
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	userID, _ := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())
	filterUserID := userID
	if isService {
		filterUserID = ""
	}

	projectID := ""
	if req.ProjectID != nil {
		projectID = *req.ProjectID
	}

	if err := s.agentDB.MoveSession(r.Context(), sessionID, projectID, filterUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found or forbidden", http.StatusNotFound)
			return
		}
		logging.Errorf("agentd: patch session %s: %v", sessionID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func dbProjectToWire(p agentdb.Project) agentproto.Project {
	files := make([]agentproto.ProjectFile, len(p.Files))
	for i, f := range p.Files {
		files[i] = agentproto.ProjectFile{Name: f.Name, Size: f.Size}
	}
	tools := p.DefaultTools
	if tools == nil {
		tools = []string{}
	}
	return agentproto.Project{
		ID:           p.ID,
		UserID:       p.UserID,
		Name:         p.Name,
		Description:  p.Description,
		Color:        p.Color,
		Glyph:        p.Glyph,
		Emoji:        p.Emoji,
		Instructions: p.Instructions,
		DefaultModel: p.DefaultModel,
		DefaultTools: tools,
		Files:        files,
		CreatedAt:    p.CreatedAt.UTC().Truncate(time.Second),
		UpdatedAt:    p.UpdatedAt.UTC().Truncate(time.Second),
	}
}

func wireFilesToDB(files []agentproto.ProjectFile) []agentdb.ProjectFile {
	out := make([]agentdb.ProjectFile, len(files))
	for i, f := range files {
		out[i] = agentdb.ProjectFile{Name: f.Name, Size: f.Size}
	}
	return out
}
