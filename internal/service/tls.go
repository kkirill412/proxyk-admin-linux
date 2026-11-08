package service

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"time"
)

// The certificate publisher atomically switches the 'current' symlink.
// Resolve it once per handshake to avoid mixing two certificate generations.
func LoadCertificate(dir, hostname string) (*tls.Certificate, error) {
	current, err := filepath.EvalSymlinks(filepath.Join(dir, "current"))
	if err != nil {
		return nil, fmt.Errorf("resolve TLS certificate: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(current, "fullchain.pem"), filepath.Join(current, "privkey.pem"))
	if err != nil {
		return nil, fmt.Errorf("load TLS key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("TLS certificate is not currently valid")
	}
	if err = leaf.VerifyHostname(hostname); err != nil {
		return nil, err
	}
	return &cert, nil
}
