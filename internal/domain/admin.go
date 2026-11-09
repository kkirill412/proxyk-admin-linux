package domain

import "errors"

var ErrAdminSessionInvalid = errors.New("invalid administrator session")

type Administrator struct {
	Username          string `json:"username"`
	PasswordHash      string `json:"-"`
	CredentialVersion int64  `json:"-"`
}

type AdminSession struct {
	Username  string `json:"username"`
	CSRFToken string `json:"csrf_token"`
}

// Credentials are written only to a protected file, never to application logs.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ServerSettings struct {
	PublicIP          string `json:"public_ip"`
	VPNPort           int    `json:"vpn_port"`
	RealityPrivateKey string `json:"reality_private_key"`
	RealityPublicKey  string `json:"reality_public_key"`
	ShortID           string `json:"short_id"`
	ServerName        string `json:"server_name"`
	SessionKey        string `json:"session_key"`
}
