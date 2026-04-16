package authd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// flowEntry tracks one in-progress Authorization Code + PKCE flow.
type flowEntry struct {
	provider        *provider
	codeVerifier    string
	expiresAt       time.Time
	loginID         string // non-empty when initiated via /oauth/login (CLI polling flow)
	zulipLinkToken  string // non-empty when initiated via /link/zulip (Zulip account linking)
}

// flowStore is an in-memory store for pending OAuth2 flows, keyed by state.
type flowStore struct {
	mu      sync.Mutex
	entries map[string]*flowEntry
}

func newFlowStore() *flowStore {
	fs := &flowStore{entries: make(map[string]*flowEntry)}
	go fs.reapLoop()
	return fs
}

// begin creates a new PKCE flow entry and returns the state token and code challenge.
func (fs *flowStore) begin(p *provider) (state, codeChallenge string, err error) {
	state, err = randomToken()
	if err != nil {
		return "", "", fmt.Errorf("authd: generate state: %w", err)
	}

	verifier, err := randomToken()
	if err != nil {
		return "", "", fmt.Errorf("authd: generate code verifier: %w", err)
	}
	codeChallenge = s256Challenge(verifier)

	fs.mu.Lock()
	fs.entries[state] = &flowEntry{
		provider:     p,
		codeVerifier: verifier,
		expiresAt:    time.Now().Add(10 * time.Minute),
	}
	fs.mu.Unlock()

	return state, codeChallenge, nil
}

// attachZulipLinkToken associates a zulip link token with an existing flow entry.
func (fs *flowStore) attachZulipLinkToken(state, token string) {
	fs.mu.Lock()
	if e, ok := fs.entries[state]; ok {
		e.zulipLinkToken = token
	}
	fs.mu.Unlock()
}

// attachLoginID associates a login_id with an existing flow entry so the
// callback can complete the corresponding polling entry.
func (fs *flowStore) attachLoginID(state, loginID string) {
	fs.mu.Lock()
	if e, ok := fs.entries[state]; ok {
		e.loginID = loginID
	}
	fs.mu.Unlock()
}

// consume retrieves and removes the flow entry for state, or returns an error
// if it doesn't exist or has expired.
func (fs *flowStore) consume(state string) (*flowEntry, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	entry, ok := fs.entries[state]
	if !ok {
		return nil, fmt.Errorf("authd: unknown or expired state")
	}
	delete(fs.entries, state)
	if time.Now().After(entry.expiresAt) {
		return nil, fmt.Errorf("authd: flow state expired")
	}
	return entry, nil
}

// reapLoop periodically removes expired entries from the store.
func (fs *flowStore) reapLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		fs.mu.Lock()
		for state, entry := range fs.entries {
			if now.After(entry.expiresAt) {
				delete(fs.entries, state)
			}
		}
		fs.mu.Unlock()
	}
}

// randomToken returns a cryptographically random 32-byte base64url string.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// s256Challenge computes the PKCE S256 code challenge from a verifier.
// challenge = BASE64URL(SHA256(verifier))
func s256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
