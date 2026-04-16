package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// sessionStore maps conversationKey → agentd session ID.
// It is the on-disk format used to persist sessions across bridge restarts.
type sessionStore map[string]string

// loadSessionStore reads the session store from path.
// Returns an empty store (not an error) if the file does not exist.
func loadSessionStore(path string) (sessionStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(sessionStore), nil
		}
		return nil, err
	}
	var store sessionStore
	if err := json.Unmarshal(data, &store); err != nil {
		// Corrupted file: return empty store rather than failing.
		return make(sessionStore), nil
	}
	return store, nil
}

// saveSessionStore atomically writes the session store to path.
// It writes to a temp file in the same directory then renames, so a crash
// mid-write cannot leave a partially-written file.
func saveSessionStore(path string, store sessionStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "zulip-sessions-*.json.tmp")
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
