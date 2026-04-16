package localidp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// signingKey holds a loaded RS256 keypair ready for use.
type signingKey struct {
	kid     string
	private *rsa.PrivateKey
}

// loadOrCreateSigningKey loads the active signing key from the DB, generating
// and persisting a new 2048-bit RS256 keypair if none exists.
func (d *db) loadOrCreateSigningKey(ctx context.Context) (*signingKey, error) {
	// Try to load existing active key.
	const q = `SELECT kid, key_pem FROM local_signing_keys WHERE active = true ORDER BY created_at DESC LIMIT 1`
	var kid, keyPEM string
	err := d.pool.QueryRow(ctx, q).Scan(&kid, &keyPEM)
	if err == nil {
		priv, parseErr := parsePrivateKeyPEM(keyPEM)
		if parseErr != nil {
			return nil, fmt.Errorf("localidp: parse stored signing key: %w", parseErr)
		}
		return &signingKey{kid: kid, private: priv}, nil
	}

	// Generate a new keypair.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("localidp: generate RSA key: %w", err)
	}

	keyPEM, err = encodePrivateKeyPEM(priv)
	if err != nil {
		return nil, fmt.Errorf("localidp: encode RSA key: %w", err)
	}

	kid = uuid.New().String()
	const ins = `INSERT INTO local_signing_keys (kid, key_pem, created_at, active) VALUES ($1, $2, $3, true)`
	if _, err := d.pool.Exec(ctx, ins, kid, keyPEM, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("localidp: persist signing key: %w", err)
	}

	return &signingKey{kid: kid, private: priv}, nil
}

func encodePrivateKeyPEM(priv *rsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return string(b), nil
}

func parsePrivateKeyPEM(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsakey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsakey, nil
}
