package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/logging"
)

// persistedSession is the on-disk format for a saved session.
type persistedSession struct {
	ID                string         `json:"id"`
	Persona           string         `json:"persona"`
	Source            string         `json:"source,omitempty"`
	Messages          []chat.Message `json:"messages"` // non-system messages only
	CreatedAt         time.Time      `json:"created_at"`
	LastInputTokens   int            `json:"last_input_tokens"`
	TotalOutputTokens int            `json:"total_output_tokens"`
}

// persistSession saves session state to DB (if configured) and/or filesystem.
// It is safe to call concurrently — it takes sess.mu internally.
func (s *Server) persistSession(sess *Session) {
	// DB path — insert new messages above the high-water mark.
	if s.agentDB != nil {
		sess.mu.Lock()
		// Collect non-system messages not yet persisted.
		var nonSystem []chat.Message
		for _, m := range sess.messages {
			if m.Role != "system" {
				nonSystem = append(nonSystem, m)
			}
		}
		// Guard against persistedMsgCount exceeding nonSystem after a clear.
		if sess.persistedMsgCount > len(nonSystem) {
			sess.persistedMsgCount = len(nonSystem)
		}
		toInsert := nonSystem[sess.persistedMsgCount:]
		startSeq := sess.persistedMsgCount
		inputToks := sess.lastInputTokens
		outputToks := sess.totalOutputTokens
		sess.mu.Unlock()

		if len(toInsert) > 0 {
			if err := s.agentDB.InsertMessages(context.Background(), sess.id, toInsert, startSeq); err != nil {
				logging.Warnf("agentd: db: insert messages for %s: %v", sess.id, err)
			} else {
				sess.mu.Lock()
				sess.persistedMsgCount = startSeq + len(toInsert)
				sess.mu.Unlock()
			}
		}
		if err := s.agentDB.UpdateStats(context.Background(), sess.id, inputToks, outputToks); err != nil {
			logging.Warnf("agentd: db: update stats for %s: %v", sess.id, err)
		}
	}

	// Filesystem fallback.
	if s.cfg != nil && s.cfg.Agentd.SessionDir != "" {
		if err := saveSession(s.cfg.Agentd.SessionDir, sess); err != nil {
			logging.Warnf("agentd: persist session %s: %v", sess.id, err)
		}
	}
}

// sessionPath returns the file path for a given session ID.
func sessionPath(dir, id string) string {
	return filepath.Join(dir, fmt.Sprintf("session_%s.json", id))
}

// saveSession persists a session's non-system messages and metadata to disk.
// Uses an atomic write (temp file + rename) to avoid partial writes.
func saveSession(dir string, sess *Session) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	sess.mu.Lock()
	ps := persistedSession{
		ID:                sess.id,
		Persona:           sess.persona,
		Source:            sess.source,
		CreatedAt:         sess.createdAt,
		LastInputTokens:   sess.lastInputTokens,
		TotalOutputTokens: sess.totalOutputTokens,
	}
	// Persist only non-system messages; system prompt is rebuilt fresh on load.
	for _, m := range sess.messages {
		if m.Role != "system" {
			ps.Messages = append(ps.Messages, m)
		}
	}
	sess.mu.Unlock()

	data, err := json.Marshal(ps)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Atomic write: write to a temp file then rename.
	tmp := sessionPath(dir, sess.id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, sessionPath(dir, sess.id)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// loadAllSessions reads all session_*.json files from dir and returns them.
// Files that fail to parse are logged and skipped.
func loadAllSessions(dir string) ([]*persistedSession, error) {
	pattern := filepath.Join(dir, "session_*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}

	var out []*persistedSession
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			logging.Warnf("agentd: persist: read %s: %v", p, err)
			continue
		}
		var ps persistedSession
		if err := json.Unmarshal(data, &ps); err != nil {
			logging.Warnf("agentd: persist: parse %s: %v", p, err)
			continue
		}
		out = append(out, &ps)
	}
	return out, nil
}
