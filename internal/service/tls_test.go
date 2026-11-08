package service_test

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
	"testing"
	"time"

	"proxyk-admin/internal/service"
)

func certificate(t *testing.T, root, name string, expired bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Now().Add(time.Hour)
	if expired {
		notAfter = time.Now().Add(-time.Minute)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		IPAddresses: []net.IP{net.ParseIP("8.8.8.8")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string][]byte{
		"fullchain.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"privkey.pem":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := service.WritePrivate(filepath.Join(dir, file), body); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTLSReloadValidityAndIP(t *testing.T) {
	dir := t.TempDir()
	certificate(t, dir, "first", false)
	certificate(t, dir, "second", false)
	certificate(t, dir, "expired", true)
	if err := os.Symlink("first", filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	first, err := service.LoadCertificate(dir, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadCertificate(dir, "8.8.4.4"); err == nil {
		t.Fatal("wrong IP accepted")
	}
	if err := os.Symlink("second", filepath.Join(dir, "next")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "next"), filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	second, err := service.LoadCertificate(dir, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("certificate did not reload")
	}
	if err := os.Symlink("expired", filepath.Join(dir, "next")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "next"), filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadCertificate(dir, "8.8.8.8"); err == nil {
		t.Fatal("expired certificate accepted")
	}
}
