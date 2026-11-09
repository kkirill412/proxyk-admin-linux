package service

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/skip2/go-qrcode"
	"proxyk-admin/internal/domain"
)

// Connections owns client export formats independently of user persistence.
type Connections struct{}

func (Connections) VLESS(user domain.VPNUser, settings domain.ServerSettings) (string, error) {
	if !user.Allowed() {
		return "", fmt.Errorf("VPN user is blocked")
	}
	query := url.Values{
		"encryption": {"none"}, "flow": {domain.VLESSFlow}, "fp": {"chrome"},
		"pbk": {settings.RealityPublicKey}, "security": {"reality"},
		"sid": {settings.ShortID}, "sni": {settings.ServerName}, "type": {"tcp"},
	}
	link := url.URL{Scheme: "vless", User: url.User(user.UUID),
		Host:     net.JoinHostPort(settings.PublicIP, strconv.Itoa(settings.VPNPort)),
		RawQuery: query.Encode(), Fragment: user.Name}
	return link.String(), nil
}

func (Connections) QR(link string) ([]byte, error) {
	// Use an integer number of pixels per module and keep the quiet zone.
	return qrcode.Encode(link, qrcode.Medium, -8)
}
