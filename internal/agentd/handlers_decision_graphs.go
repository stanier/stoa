package agentd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/logging"
)

// Saved decision graphs (the web UI's Decision graphs screen).
//
//	GET    /decision-graphs       list the caller's graphs (no documents)
//	POST   /decision-graphs       create {name, graph}
//	GET    /decision-graphs/:id   one graph with its document
//	PUT    /decision-graphs/:id   replace {name, graph}
//	DELETE /decision-graphs/:id
//
// Graphs belong to the user in the auth context, never to a session, so they
// survive agentd restarts. Users see only their own; service accounts see
// all, as with projects.

const (
	maxDecisionGraphBytes    = 256 << 10
	maxDecisionGraphNameLen  = 200
	maxDecisionGraphsPerUser = 500
)

type decisionGraphWire struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Graph     json.RawMessage `json:"graph,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func decisionGraphToWire(g agentdb.DecisionGraph) decisionGraphWire {
	return decisionGraphWire{ID: g.ID, Name: g.Name, Graph: g.Graph, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}

// decisionGraphCaller returns the owner to record on new graphs and the
// owner filter for reads and writes ("" = no filter, for service accounts).
// ok is false when a write has no one to own it.
func decisionGraphCaller(r *http.Request, write bool) (owner, filter string, ok bool) {
	userID, hasUser := UserIDFromContext(r.Context())
	if IsServiceFromContext(r.Context()) {
		return userID, "", true
	}
	if write && !hasUser {
		return "", "", false
	}
	return userID, userID, true
}

// readDecisionGraphBody parses {name, graph}: name is trimmed and bounded
// (empty becomes "Untitled graph"), graph must be a JSON object.
func readDecisionGraphBody(w http.ResponseWriter, r *http.Request) (string, json.RawMessage, string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDecisionGraphBytes+4<<10))
	if err != nil {
		return "", nil, "graph too large"
	}
	var req struct {
		Name  string          `json:"name"`
		Graph json.RawMessage `json:"graph"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, "invalid JSON body"
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Untitled graph"
	}
	if utf8.RuneCountInString(name) > maxDecisionGraphNameLen {
		return "", nil, "name too long"
	}
	g := bytes.TrimSpace(req.Graph)
	if len(g) == 0 || g[0] != '{' {
		return "", nil, "graph must be a JSON object"
	}
	if len(g) > maxDecisionGraphBytes {
		return "", nil, "graph too large"
	}
	return name, json.RawMessage(g), ""
}

func (s *Server) handleDecisionGraphs(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		_, filter, _ := decisionGraphCaller(r, false)
		list, err := s.agentDB.ListDecisionGraphs(r.Context(), filter)
		if err != nil {
			logging.Warnf("agentd: list decision graphs: %v", err)
			http.Error(w, "failed to list decision graphs", http.StatusInternalServerError)
			return
		}
		out := make([]decisionGraphWire, 0, len(list))
		for _, g := range list {
			out = append(out, decisionGraphToWire(g))
		}
		writeJSONResponse(w, http.StatusOK, out)

	case http.MethodPost:
		owner, _, ok := decisionGraphCaller(r, true)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name, graph, msg := readDecisionGraphBody(w, r)
		if msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		if owner != "" {
			n, err := s.agentDB.CountDecisionGraphs(r.Context(), owner)
			if err != nil {
				logging.Warnf("agentd: count decision graphs: %v", err)
				http.Error(w, "failed to create decision graph", http.StatusInternalServerError)
				return
			}
			if n >= maxDecisionGraphsPerUser {
				http.Error(w, "too many decision graphs", http.StatusConflict)
				return
			}
		}
		g := agentdb.DecisionGraph{ID: "dg_" + uuid.NewString(), UserID: owner, Name: name, Graph: graph}
		if err := s.agentDB.CreateDecisionGraph(r.Context(), g); err != nil {
			logging.Warnf("agentd: create decision graph: %v", err)
			http.Error(w, "failed to create decision graph", http.StatusInternalServerError)
			return
		}
		created, err := s.agentDB.GetDecisionGraph(r.Context(), g.ID, "")
		if err != nil || created == nil {
			http.Error(w, "failed to read back decision graph", http.StatusInternalServerError)
			return
		}
		writeJSONResponse(w, http.StatusCreated, decisionGraphToWire(*created))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDecisionGraphByID(w http.ResponseWriter, r *http.Request) {
	if s.agentDB == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/decision-graphs/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		_, filter, _ := decisionGraphCaller(r, false)
		g, err := s.agentDB.GetDecisionGraph(r.Context(), id, filter)
		if err != nil {
			logging.Warnf("agentd: get decision graph %q: %v", id, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if g == nil {
			http.Error(w, "decision graph not found", http.StatusNotFound)
			return
		}
		writeJSONResponse(w, http.StatusOK, decisionGraphToWire(*g))

	case http.MethodPut:
		_, filter, ok := decisionGraphCaller(r, true)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name, graph, msg := readDecisionGraphBody(w, r)
		if msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		err := s.agentDB.UpdateDecisionGraph(r.Context(), agentdb.DecisionGraph{ID: id, UserID: filter, Name: name, Graph: graph})
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "decision graph not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logging.Warnf("agentd: update decision graph %q: %v", id, err)
			http.Error(w, "failed to update decision graph", http.StatusInternalServerError)
			return
		}
		updated, err := s.agentDB.GetDecisionGraph(r.Context(), id, "")
		if err != nil || updated == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSONResponse(w, http.StatusOK, decisionGraphToWire(*updated))

	case http.MethodDelete:
		_, filter, ok := decisionGraphCaller(r, true)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		err := s.agentDB.DeleteDecisionGraph(r.Context(), id, filter)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "decision graph not found", http.StatusNotFound)
			return
		}
		if err != nil {
			logging.Warnf("agentd: delete decision graph %q: %v", id, err)
			http.Error(w, "failed to delete decision graph", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSONResponse(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
