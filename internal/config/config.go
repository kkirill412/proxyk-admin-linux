package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	DataDir, TLSDir, XrayDir, ListenAddr, XrayAddr string
	PublicIP                                       string
	VPNPort                                        int
}

func Load() (Config, error) {
	c := Config{
		DataDir:    env("DATA_DIR", "/var/lib/proxyk-admin"),
		TLSDir:     env("TLS_DIR", "/etc/proxyk-admin/tls"),
		XrayDir:    env("XRAY_CONFIG_DIR", "/etc/proxyk-admin/xray"),
		ListenAddr: env("LISTEN_ADDR", ":8443"),
		XrayAddr:   env("XRAY_ADDR", "xray:10085"),
		PublicIP:   os.Getenv("PUBLIC_IP"),
	}
	var err error
	c.VPNPort, err = strconv.Atoi(env("VPN_PORT", "443"))
	if err != nil || c.VPNPort < 1 || c.VPNPort > 65535 {
		return c, fmt.Errorf("VPN_PORT must be between 1 and 65535")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return c, fmt.Errorf("invalid LISTEN_ADDR: %w", err)
	}
	if _, _, err := net.SplitHostPort(c.XrayAddr); err != nil {
		return c, fmt.Errorf("invalid XRAY_ADDR: %w", err)
	}
	if c.PublicIP != "" {
		ip, err := netip.ParseAddr(c.PublicIP)
		if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return c, fmt.Errorf("PUBLIC_IP must be a public IPv4 address")
		}
	}
	return c, nil
}

func (c Config) DatabasePath() string    { return filepath.Join(c.DataDir, "app.db") }
func (c Config) CredentialsPath() string { return filepath.Join(c.DataDir, "initial-credentials.json") }
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
