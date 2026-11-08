package service

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
	"proxyk-admin/internal/domain"
)

func RandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func NewCredentials() (domain.Credentials, string, error) {
	name, err := RandomToken(9)
	if err != nil {
		return domain.Credentials{}, "", err
	}
	password, err := RandomToken(24)
	if err != nil {
		return domain.Credentials{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return domain.Credentials{Username: "admin_" + name, Password: password}, string(hash), err
}

func NewSettings(ip string, port int) (domain.ServerSettings, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return domain.ServerSettings{}, err
	}
	shortID := make([]byte, 8)
	if _, err = rand.Read(shortID); err != nil {
		return domain.ServerSettings{}, err
	}
	secret, err := RandomToken(32)
	if err != nil {
		return domain.ServerSettings{}, err
	}
	return domain.ServerSettings{
		PublicIP: ip, VPNPort: port,
		RealityPrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()),
		RealityPublicKey:  base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		ShortID:           hex.EncodeToString(shortID), ServerName: "www.cloudflare.com", SessionKey: secret,
	}, nil
}
