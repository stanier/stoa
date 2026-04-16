package matrix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"maunium.net/go/mautrix/id"
)

// FileSyncStore implements mautrix.SyncStore by persisting the next_batch token
// to a file on disk. Filter IDs are kept in memory only (they are cheap to
// re-request on each startup).
type FileSyncStore struct {
	path string

	// in-memory filter ID store (not persisted)
	filterIDs map[id.UserID]string
}

// SaveFilterID stores the filter ID in memory.
func (s *FileSyncStore) SaveFilterID(_ context.Context, userID id.UserID, filterID string) error {
	if s.filterIDs == nil {
		s.filterIDs = make(map[id.UserID]string)
	}
	s.filterIDs[userID] = filterID
	return nil
}

// LoadFilterID returns the in-memory filter ID for userID.
func (s *FileSyncStore) LoadFilterID(_ context.Context, userID id.UserID) (string, error) {
	if s.filterIDs == nil {
		return "", nil
	}
	return s.filterIDs[userID], nil
}

// SaveNextBatch writes the next_batch token to s.path atomically (temp file +
// rename). The userID is ignored because this store is single-user.
func (s *FileSyncStore) SaveNextBatch(_ context.Context, _ id.UserID, nextBatchToken string) error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "matrix-sync-token-*.tmp")
	if err != nil {
		return fmt.Errorf("matrix sync store: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(nextBatchToken); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("matrix sync store: write token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("matrix sync store: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("matrix sync store: rename token file: %w", err)
	}
	return nil
}

// LoadNextBatch reads the next_batch token from s.path. Returns "" if the
// file does not exist yet (first run).
func (s *FileSyncStore) LoadNextBatch(_ context.Context, _ id.UserID) (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("matrix sync store: read token file: %w", err)
	}
	return string(data), nil
}
