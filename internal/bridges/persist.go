// Package bridges provides shared utilities for bridge processes.
package bridges

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SessionStore maps conversationKey → agentd session ID.
type SessionStore map[string]string

// LoadSessionStore reads a session store from path.
// Returns an empty store (not an error) if the file does not exist or is corrupted.
func LoadSessionStore(path string) (SessionStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(SessionStore), nil
		}
		return nil, err
	}
	var store SessionStore
	if err := json.Unmarshal(data, &store); err != nil {
		// Corrupted file: return empty store rather than failing.
		return make(SessionStore), nil
	}
	return store, nil
}

// SaveSessionStore atomically writes the session store to path using the given
// temp file prefix (e.g. "slack-sessions-"). It writes to a temp file in the
// same directory then renames, so a crash mid-write cannot leave a partial file.
func SaveSessionStore(path, tmpPrefix string, store SessionStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, tmpPrefix+"*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, path)
}
