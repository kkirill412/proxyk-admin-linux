package usecase

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
)

type VPNUsers struct{ Store *sqlite.Store }

func (u VPNUsers) Create(ctx context.Context, name string) (domain.VPNUser, error) {
	name, err := vpnUserName(name)
	if err != nil {
		return domain.VPNUser{}, err
	}
	credential, err := uuid.NewRandom()
	if err != nil {
		return domain.VPNUser{}, err
	}
	return u.Store.CreateVPNUser(ctx, domain.VPNUser{UUID: credential.String(), Name: name, Enabled: true})
}

func (u VPNUsers) Rename(ctx context.Context, id int64, name string) error {
	name, err := vpnUserName(name)
	if err != nil {
		return err
	}
	return u.Store.RenameVPNUser(ctx, id, name)
}

func (u VPNUsers) Link(ctx context.Context, id int64) (string, error) {
	user, err := u.Store.VPNUser(ctx, id)
	if err != nil {
		return "", err
	}
	if !user.Enabled {
		return "", fmt.Errorf("VPN user %d is disabled", id)
	}
	settings, err := u.Store.ServerSettings(ctx)
	if err != nil {
		return "", err
	}
	query := url.Values{
		"encryption": {"none"},
		"flow":       {domain.VLESSFlow},
		"fp":         {"chrome"},
		"pbk":        {settings.RealityPublicKey},
		"security":   {"reality"},
		"sid":        {settings.ShortID},
		"sni":        {settings.ServerName},
		"type":       {"tcp"},
	}
	link := url.URL{
		Scheme:   "vless",
		User:     url.User(user.UUID),
		Host:     net.JoinHostPort(settings.PublicIP, strconv.Itoa(settings.VPNPort)),
		RawQuery: query.Encode(),
		Fragment: user.Name,
	}
	return link.String(), nil
}

func vpnUserName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("VPN user name must contain 1 to 80 characters without control characters")
	}
	return name, nil
}
