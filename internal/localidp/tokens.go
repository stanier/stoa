package localidp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// issueIDToken produces a signed RS256 JWT for the given user.
func issueIDToken(key *signingKey, issuer, clientID, sub, email, name string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()

	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": key.kid,
	}
	payload := map[string]interface{}{
		"iss":   issuer,
		"sub":   sub,
		"aud":   clientID,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
		"email": email,
		"name":  name,
	}

	hdr, err := jsonBase64(header)
	if err != nil {
		return "", err
	}
	pay, err := jsonBase64(payload)
	if err != nil {
		return "", err
	}

	sigInput := hdr + "." + pay
	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key.private, crypto.SHA256, h[:])
	if err != nil {
		return "", fmt.Errorf("localidp: sign JWT: %w", err)
	}

	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// issueAccessToken produces a simple opaque access token (random bytes).
// local-idp uses this to satisfy the OAuth2 token endpoint contract; it is
// not a JWT. The userinfo endpoint accepts it by looking up the session.
func issueOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// jwksJSON returns the JWKS JSON document for the active signing key.
func jwksJSON(key *signingKey) ([]byte, error) {
	pub := &key.private.PublicKey
	return json.Marshal(map[string]interface{}{
		"keys": []map[string]interface{}{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": key.kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	})
}

func jsonBase64(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseSubFromAccessToken extracts the sub claim from the opaque token stored
// in the in-memory access token → user map maintained by the server.
// (This is just a type alias for clarity at call sites.)
type accessTokenStore = map[string]accessTokenEntry

type accessTokenEntry struct {
	sub       string
	email     string
	name      string
	expiresAt time.Time
}

// splitJWTParts splits a JWT into its three dot-separated parts.
func splitJWTParts(token string) (header, payload, sig string, ok bool) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
