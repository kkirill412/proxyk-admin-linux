package service_test

import (
	"bytes"
	"image/png"
	"net/url"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

func TestConnectionQRRoundTrip(t *testing.T) {
	user := domain.VPNUser{UUID: "1b214a31-776c-40e0-95a1-1dd71d57f4a0", Name: "Телефон & /?", Enabled: true}
	settings := domain.ServerSettings{PublicIP: "8.8.8.8", VPNPort: 8443, RealityPublicKey: "public-key", ShortID: "1234abcd", ServerName: "www.cloudflare.com"}
	connections := service.Connections{}
	link, err := connections.VLESS(user, settings)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Fragment != user.Name || parsed.User.Username() != user.UUID || parsed.Host != "8.8.8.8:8443" {
		t.Fatal("connection lost escaped label or credentials")
	}
	data, err := connections.QR(link)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil || decoded.GetText() != link {
		t.Fatalf("QR does not contain exact connection URI: %v", err)
	}
	user.Enabled = false
	if _, err := connections.VLESS(user, settings); err == nil {
		t.Fatal("blocked user exported")
	}
}
