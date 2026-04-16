package agentd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/stanier/stoa/internal/logging"
)

// EnsureSelfSignedCert checks whether certFile and keyFile already exist.
// If both exist, it returns their paths unchanged.
// If either is missing, it generates a new ECDSA P-256 self-signed certificate
// valid for 10 years, writes the files with mode 0600, and returns their paths.
//
// The certificate covers localhost, 127.0.0.1, and ::1 as SANs so that local
// clients connecting to agentd can verify it without extra configuration.
func EnsureSelfSignedCert(certFile, keyFile string) (string, string, error) {
	// If both files already exist, reuse them.
	if fileExists(certFile) && fileExists(keyFile) {
		logging.Infof("agentd: using existing TLS cert %s", certFile)
		return certFile, keyFile, nil
	}

	logging.Infof("agentd: generating self-signed TLS certificate → %s", certFile)

	// Generate ECDSA P-256 key pair.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"stoa agentd (self-signed)"},
			CommonName:   "localhost",
		},
		NotBefore:             time.Now().Add(-time.Minute), // small skew tolerance
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}

	if err := os.MkdirAll(filepath.Dir(certFile), 0700); err != nil {
		return "", "", err
	}

	// Write certificate PEM.
	cf, err := os.OpenFile(certFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", "", err
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		cf.Close()
		return "", "", err
	}
	cf.Close()

	// Write private key PEM.
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	kf, err := os.OpenFile(keyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", "", err
	}
	if err := pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}); err != nil {
		kf.Close()
		return "", "", err
	}
	kf.Close()

	logging.Infof("agentd: self-signed cert generated (valid 10 years, SANs: localhost, 127.0.0.1, ::1)")
	return certFile, keyFile, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
