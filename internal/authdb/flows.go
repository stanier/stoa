package authdb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OAuthFlow holds the data retrieved when consuming a PKCE flow entry.
type OAuthFlow struct {
	ProviderID     string
	CodeVerifier   string
	LoginID        string
	ZulipLinkToken string
}

// ZulipLinkEntry is the data stored for a pending Zulip account link token.
type ZulipLinkEntry struct {
	ZulipEmail string
	// UserID is set only on a pending confirmation (see
	// CreateZulipLinkConfirmation): the authd user the link would bind to.
	UserID string
}

// BeginOAuthFlow generates a state token and PKCE code verifier, persists the
// flow entry, and returns state and codeChallenge. ttl governs how long the
// entry lives before the callback must arrive.
func (db *DB) BeginOAuthFlow(ctx context.Context, providerID string, ttl time.Duration) (state, codeChallenge string, err error) {
	state, err = generateToken()
	if err != nil {
		return "", "", fmt.Errorf("authdb: generate state: %w", err)
	}
	verifier, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("authdb: generate code verifier: %w", err)
	}
	codeChallenge = s256Challenge(verifier)

	const q = `
		INSERT INTO oauth_flows (state, provider_id, code_verifier, expires_at)
		VALUES ($1, $2, $3, $4)`
	if _, err = db.pool.Exec(ctx, q, state, providerID, verifier, time.Now().Add(ttl)); err != nil {
		return "", "", fmt.Errorf("authdb: begin oauth flow: %w", err)
	}
	return state, codeChallenge, nil
}

// UpdateOAuthFlowLoginID attaches a login_id to an existing flow entry so the
// callback can complete the polling login.
func (db *DB) UpdateOAuthFlowLoginID(ctx context.Context, state, loginID string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE oauth_flows SET login_id = $1 WHERE state = $2`, loginID, state)
	return err
}

// AttachZulipLinkToken sets the zulip_link_token on an existing flow entry.
func (db *DB) AttachZulipLinkToken(ctx context.Context, state, token string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE oauth_flows SET zulip_link_token = $1 WHERE state = $2`, token, state)
	return err
}

// ConsumeOAuthFlow retrieves and deletes the flow entry for state.
// Returns ErrNotFound if no entry exists, ErrExpired if the entry has passed its TTL.
func (db *DB) ConsumeOAuthFlow(ctx context.Context, state string) (*OAuthFlow, error) {
	const q = `
		DELETE FROM oauth_flows WHERE state = $1
		RETURNING provider_id, code_verifier, login_id, zulip_link_token, expires_at`
	var f OAuthFlow
	var expiresAt time.Time
	err := db.pool.QueryRow(ctx, q, state).Scan(
		&f.ProviderID, &f.CodeVerifier, &f.LoginID, &f.ZulipLinkToken, &expiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("unknown or expired state")
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: consume oauth flow: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("flow state expired")
	}
	return &f, nil
}

// CreateLoginFlow stores a pending browser login entry and returns its ID.
// The entry is polled by the CLI via ConsumeLoginFlow until it completes or expires.
func (db *DB) CreateLoginFlow(ctx context.Context, providerID, authURL, postLoginRedirect string, ttl time.Duration) (string, error) {
	id, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("authdb: generate login flow id: %w", err)
	}
	const q = `
		INSERT INTO login_flows (id, provider_id, auth_url, post_login_redirect, expires_at)
		VALUES ($1, $2, $3, $4, $5)`
	if _, err = db.pool.Exec(ctx, q, id, providerID, authURL, postLoginRedirect, time.Now().Add(ttl)); err != nil {
		return "", fmt.Errorf("authdb: create login flow: %w", err)
	}
	return id, nil
}

// CompleteLoginFlow records the session token and LLM key for a confirmed
// terminal (polling) login. Web flows use CompleteWebLoginFlow instead.
// Returns ErrNotFound if id does not exist or is a web flow.
func (db *DB) CompleteLoginFlow(ctx context.Context, id, sessionToken, llmKey string) (string, error) {
	var postLoginRedirect string
	err := db.pool.QueryRow(ctx,
		`UPDATE login_flows SET session_token=$1, llm_key=$2 WHERE id=$3 AND post_login_redirect = '' RETURNING post_login_redirect`,
		sessionToken, llmKey, id,
	).Scan(&postLoginRedirect)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authdb: complete login flow: %w", err)
	}
	return postLoginRedirect, nil
}

// FailLoginFlow records a failure reason on a pending login entry so the polling
// client receives a clean error.
func (db *DB) FailLoginFlow(ctx context.Context, id, errMsg string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE login_flows SET err_msg=$1 WHERE id=$2`, errMsg, id)
	return err
}

// ConsumeLoginFlow returns the current state of the login flow. done=false means
// the browser flow has not yet completed. When done=true and errMsg is empty,
// sessionToken and llmKey are populated. The entry is deleted once complete or failed.
//
// Web flows (those with a post_login_redirect) can't be polled: their token is
// only released to ExchangeLoginCode, so knowing a login_id is not enough to
// collect someone else's session.
func (db *DB) ConsumeLoginFlow(ctx context.Context, id string) (sessionToken, llmKey, errMsg string, done bool, err error) {
	const q = `SELECT session_token, llm_key, err_msg, post_login_redirect, expires_at FROM login_flows WHERE id = $1`
	var expiresAt time.Time
	var postLoginRedirect string
	err = db.pool.QueryRow(ctx, q, id).Scan(&sessionToken, &llmKey, &errMsg, &postLoginRedirect, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "unknown login_id", true, nil
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("authdb: consume login flow: %w", err)
	}
	if time.Now().After(expiresAt) {
		_, _ = db.pool.Exec(ctx, `DELETE FROM login_flows WHERE id = $1`, id)
		return "", "", "login expired", true, nil
	}
	if postLoginRedirect != "" {
		return "", "", "browser login flows cannot be polled", true, nil
	}
	if sessionToken != "" || errMsg != "" {
		_, _ = db.pool.Exec(ctx, `DELETE FROM login_flows WHERE id = $1`, id)
		return sessionToken, llmKey, errMsg, true, nil
	}
	return "", "", "", false, nil
}

// LoginFlowRedirect returns the post_login_redirect of a login flow ("" for a
// terminal/polling flow). Returns ErrNotFound if id does not exist.
func (db *DB) LoginFlowRedirect(ctx context.Context, id string) (string, error) {
	var redirect string
	err := db.pool.QueryRow(ctx,
		`SELECT post_login_redirect FROM login_flows WHERE id = $1`, id).Scan(&redirect)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authdb: login flow redirect: %w", err)
	}
	return redirect, nil
}

// CompleteWebLoginFlow records the session token and LLM key for a finished
// browser (post_login_redirect) login and returns a one-time exchange code.
// Only its hash is stored; the token is released by ExchangeLoginCode.
func (db *DB) CompleteWebLoginFlow(ctx context.Context, id, sessionToken, llmKey string) (string, error) {
	code, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("authdb: generate login exchange code: %w", err)
	}
	tag, err := db.pool.Exec(ctx,
		`UPDATE login_flows SET session_token=$1, llm_key=$2, exchange_code_hash=$3
		 WHERE id=$4 AND post_login_redirect <> ''`,
		sessionToken, llmKey, hashToken(code), id)
	if err != nil {
		return "", fmt.Errorf("authdb: complete web login flow: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return code, nil
}

// ExchangeLoginCode releases (and deletes) the session token of a completed
// browser login, given its one-time exchange code. Returns ErrNotFound when
// the id/code pair doesn't match a completed, unexpired flow.
func (db *DB) ExchangeLoginCode(ctx context.Context, id, code string) (sessionToken, llmKey string, err error) {
	if id == "" || code == "" {
		return "", "", ErrNotFound
	}
	err = db.pool.QueryRow(ctx,
		`DELETE FROM login_flows
		 WHERE id=$1 AND exchange_code_hash=$2 AND session_token <> '' AND expires_at > now()
		 RETURNING session_token, llm_key`,
		id, hashToken(code)).Scan(&sessionToken, &llmKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("authdb: exchange login code: %w", err)
	}
	return sessionToken, llmKey, nil
}

// BeginLoginConfirmation records that userID signed in for a terminal login
// flow and returns a one-time nonce the browser must present to confirm it.
// No session exists until TakeLoginConfirmation succeeds.
func (db *DB) BeginLoginConfirmation(ctx context.Context, id, userID string) (string, error) {
	nonce, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("authdb: generate login confirmation: %w", err)
	}
	tag, err := db.pool.Exec(ctx,
		`UPDATE login_flows SET confirm_user_id=$1, confirm_nonce_hash=$2
		 WHERE id=$3 AND post_login_redirect = '' AND session_token = '' AND err_msg = '' AND expires_at > now()`,
		userID, hashToken(nonce), id)
	if err != nil {
		return "", fmt.Errorf("authdb: begin login confirmation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return nonce, nil
}

// TakeLoginConfirmation checks a confirmation nonce for a terminal login flow
// and returns the user it was for. The confirmation is single-use.
func (db *DB) TakeLoginConfirmation(ctx context.Context, id, nonce string) (string, error) {
	if id == "" || nonce == "" {
		return "", ErrNotFound
	}
	var userID string
	err := db.pool.QueryRow(ctx,
		`UPDATE login_flows f SET confirm_user_id='', confirm_nonce_hash=''
		 FROM (SELECT id, confirm_user_id FROM login_flows WHERE id=$1 FOR UPDATE) old
		 WHERE f.id = old.id AND f.confirm_nonce_hash=$2 AND old.confirm_user_id <> '' AND f.expires_at > now()
		 RETURNING old.confirm_user_id`,
		id, hashToken(nonce)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authdb: take login confirmation: %w", err)
	}
	return userID, nil
}

// CreateZulipLinkToken stores a short-lived Zulip account link token.
func (db *DB) CreateZulipLinkToken(ctx context.Context, zulipEmail string, ttl time.Duration) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("authdb: generate zulip link token: %w", err)
	}
	_, err = db.pool.Exec(ctx,
		`INSERT INTO zulip_link_tokens (token, zulip_email, expires_at) VALUES ($1, $2, $3)`,
		token, zulipEmail, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("authdb: create zulip link token: %w", err)
	}
	return token, nil
}

// PeekZulipLinkToken reads the token entry without deleting it.
// Returns ErrNotFound if missing, ErrExpired if stale.
func (db *DB) PeekZulipLinkToken(ctx context.Context, token string) (*ZulipLinkEntry, error) {
	var e ZulipLinkEntry
	var expiresAt time.Time
	err := db.pool.QueryRow(ctx,
		`SELECT zulip_email, expires_at FROM zulip_link_tokens WHERE token = $1 AND user_id = ''`,
		token).Scan(&e.ZulipEmail, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: peek zulip link token: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, ErrExpired
	}
	return &e, nil
}

// ConsumeZulipLinkToken reads and deletes the token entry.
// Returns ErrNotFound if missing, ErrExpired if stale.
func (db *DB) ConsumeZulipLinkToken(ctx context.Context, token string) (*ZulipLinkEntry, error) {
	var e ZulipLinkEntry
	var expiresAt time.Time
	err := db.pool.QueryRow(ctx,
		`DELETE FROM zulip_link_tokens WHERE token = $1 AND user_id = '' RETURNING zulip_email, expires_at`,
		token).Scan(&e.ZulipEmail, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: consume zulip link token: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, ErrExpired
	}
	return &e, nil
}

// CreateZulipLinkConfirmation stores a pending Zulip link for userID and
// returns a one-time nonce the user must present (form field and cookie) to
// confirm it.
func (db *DB) CreateZulipLinkConfirmation(ctx context.Context, zulipEmail, userID string, ttl time.Duration) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("authdb: zulip link confirmation needs a user_id")
	}
	nonce, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("authdb: generate zulip link confirmation: %w", err)
	}
	_, err = db.pool.Exec(ctx,
		`INSERT INTO zulip_link_tokens (token, zulip_email, user_id, expires_at) VALUES ($1, $2, $3, $4)`,
		nonce, zulipEmail, userID, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("authdb: create zulip link confirmation: %w", err)
	}
	return nonce, nil
}

// ConsumeZulipLinkConfirmation reads and deletes a pending confirmation.
// Returns ErrNotFound if missing, ErrExpired if stale.
func (db *DB) ConsumeZulipLinkConfirmation(ctx context.Context, nonce string) (*ZulipLinkEntry, error) {
	var e ZulipLinkEntry
	var expiresAt time.Time
	err := db.pool.QueryRow(ctx,
		`DELETE FROM zulip_link_tokens WHERE token = $1 AND user_id <> '' RETURNING zulip_email, user_id, expires_at`,
		nonce).Scan(&e.ZulipEmail, &e.UserID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: consume zulip link confirmation: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, ErrExpired
	}
	return &e, nil
}

// s256Challenge computes the PKCE S256 code challenge from a verifier.
// challenge = BASE64URL(SHA256(verifier))
func s256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
