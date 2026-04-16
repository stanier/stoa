package localidp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// authCode tracks one pending Authorization Code grant.
type authCode struct {
	userID       string
	sub          string
	email        string
	name         string
	clientID     string
	redirectURI  string
	codeChallenge string // S256 — already hashed by the client
	expiresAt    time.Time
}

// codeStore is an in-memory store for pending authorization codes.
// Codes are short-lived (10 minutes) and single-use.
type codeStore struct {
	mu      sync.Mutex
	entries map[string]*authCode // codeHash → entry
}

func newCodeStore() *codeStore {
	cs := &codeStore{entries: make(map[string]*authCode)}
	go cs.reapLoop()
	return cs
}

// issue generates a new authorization code, stores the entry, and returns the raw code.
func (cs *codeStore) issue(entry *authCode) (string, error) {
	raw, err := randomBytes(32)
	if err != nil {
		return "", fmt.Errorf("localidp: generate auth code: %w", err)
	}
	entry.expiresAt = time.Now().Add(10 * time.Minute)

	cs.mu.Lock()
	cs.entries[hashCode(raw)] = entry
	cs.mu.Unlock()

	return raw, nil
}

// consume retrieves and deletes the entry for raw, validating expiry and PKCE.
func (cs *codeStore) consume(raw, codeVerifier string) (*authCode, error) {
	hash := hashCode(raw)

	cs.mu.Lock()
	entry, ok := cs.entries[hash]
	if ok {
		delete(cs.entries, hash)
	}
	cs.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("localidp: unknown or expired authorization code")
	}
	if time.Now().After(entry.expiresAt) {
		return nil, fmt.Errorf("localidp: authorization code expired")
	}

	// Verify PKCE S256: SHA256(verifier) must equal stored challenge.
	h := sha256.Sum256([]byte(codeVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	if challenge != entry.codeChallenge {
		return nil, fmt.Errorf("localidp: PKCE verification failed")
	}

	return entry, nil
}

func (cs *codeStore) reapLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		cs.mu.Lock()
		for k, v := range cs.entries {
			if now.After(v.expiresAt) {
				delete(cs.entries, k)
			}
		}
		cs.mu.Unlock()
	}
}

func hashCode(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func randomBytes(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
