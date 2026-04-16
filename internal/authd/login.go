package authd

import (
	"sync"
	"time"
)

// loginEntry tracks one pending browser-based login initiated by a CLI client
// or a web UI BFF.
type loginEntry struct {
	providerID  string
	authURL     string
	expiresAt   time.Time

	// postLoginRedirect is an optional URL to redirect the browser to after a
	// successful callback, with ?token=<session_token> appended.
	// Used by web UI BFFs so the browser lands back on the app rather than
	// authd's "you may close this tab" page.
	// Must be validated against AUTHD_ALLOWED_REDIRECT_ORIGINS before storing.
	postLoginRedirect string

	// Set by the callback handler on success.
	sessionToken string
	// Set by the callback handler on failure.
	errMsg string
}

// loginStore is an in-memory map of login_id → pending login.
type loginStore struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

func newLoginStore() *loginStore {
	ls := &loginStore{entries: make(map[string]*loginEntry)}
	go ls.reapLoop()
	return ls
}

// create stores a new pending login and returns its ID.
// postLoginRedirect may be empty (CLI polling flow) or a validated URL
// (web BFF flow) — caller is responsible for validation before passing it in.
func (ls *loginStore) create(providerID, authURL, postLoginRedirect string, ttl time.Duration) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	ls.mu.Lock()
	ls.entries[id] = &loginEntry{
		providerID:        providerID,
		authURL:           authURL,
		postLoginRedirect: postLoginRedirect,
		expiresAt:         time.Now().Add(ttl),
	}
	ls.mu.Unlock()
	return id, nil
}

// get returns the entry for id without removing it.
func (ls *loginStore) get(id string) (*loginEntry, bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.entries[id]
	return e, ok
}

// complete marks the login as done and deposits the session token.
// Called by the OAuth callback after issuing a session.
func (ls *loginStore) complete(id, sessionToken string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.entries[id]
	if !ok {
		return false
	}
	e.sessionToken = sessionToken
	return true
}

// fail marks the login as failed (e.g. provider returned an error).
func (ls *loginStore) fail(id, msg string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.entries[id]
	if !ok {
		return false
	}
	e.errMsg = msg
	return true
}

// consume retrieves and deletes a completed or failed entry.
// Returns ("", "", false) when the login is still pending.
func (ls *loginStore) consume(id string) (sessionToken, errMsg string, done bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.entries[id]
	if !ok {
		return "", "unknown login_id", true
	}
	if time.Now().After(e.expiresAt) {
		delete(ls.entries, id)
		return "", "login expired", true
	}
	if e.sessionToken != "" {
		delete(ls.entries, id)
		return e.sessionToken, "", true
	}
	if e.errMsg != "" {
		delete(ls.entries, id)
		return "", e.errMsg, true
	}
	return "", "", false
}

func (ls *loginStore) reapLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		ls.mu.Lock()
		for id, e := range ls.entries {
			if now.After(e.expiresAt) {
				delete(ls.entries, id)
			}
		}
		ls.mu.Unlock()
	}
}
