package main

import "github.com/stanier/stoa/internal/bridges"

// sessionStore maps conversationKey → agentd session ID.
// It is the on-disk format used to persist sessions across bridge restarts.
type sessionStore = bridges.SessionStore

func loadSessionStore(path string) (sessionStore, error) {
	return bridges.LoadSessionStore(path)
}

func saveSessionStore(path string, store sessionStore) error {
	return bridges.SaveSessionStore(path, "discord-sessions-", store)
}
