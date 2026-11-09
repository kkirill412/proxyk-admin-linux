package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

type VPNUserConfirmation interface {
	ConfirmUser(context.Context, domain.VPNUser) error
}

type VPNUsers struct {
	Store *sqlite.Store
	Xray  VPNUserConfirmation
}

func (u VPNUsers) Create(ctx context.Context, name string, limit *int64) (domain.VPNUser, error) {
	name, err := vpnUserName(name)
	if err != nil {
		return domain.VPNUser{}, err
	}
	if limit != nil && *limit < 0 {
		return domain.VPNUser{}, domain.ErrInvalidTrafficLimit
	}
	credential, err := uuid.NewRandom()
	if err != nil {
		return domain.VPNUser{}, err
	}
	user, err := u.Store.CreateVPNUser(ctx, domain.VPNUser{UUID: credential.String(), Name: name, Enabled: true, TrafficLimitBytes: limit})
	if err != nil {
		return user, err
	}
	if err := u.Wait(ctx, user.ID, false); err != nil {
		return user, err
	}
	confirmed, err := u.Store.VPNUser(ctx, user.ID)
	if err != nil {
		return user, err
	}
	return confirmed, nil
}

func (u VPNUsers) Rename(ctx context.Context, id int64, name string) error {
	name, err := vpnUserName(name)
	if err != nil {
		return err
	}
	return u.Store.RenameVPNUser(ctx, id, name)
}

func (u VPNUsers) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := u.Store.SetVPNUserEnabled(ctx, id, enabled); err != nil {
		return err
	}
	return u.Wait(ctx, id, false)
}

func (u VPNUsers) SetLimit(ctx context.Context, id int64, limit *int64) error {
	if err := u.Store.SetVPNUserLimit(ctx, id, limit); err != nil {
		return err
	}
	return u.Wait(ctx, id, false)
}

func (u VPNUsers) Delete(ctx context.Context, id int64) error {
	if err := u.Store.DeleteVPNUser(ctx, id); err != nil {
		return err
	}
	return u.Wait(ctx, id, true)
}

// Wait is bounded even for a CLI without a deadline. Pending changes remain in
// SQLite and will be retried by the server; a failed create must not be repeated.
func (u VPNUsers) Wait(ctx context.Context, id int64, deleted bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		user, err := u.Store.VPNUser(ctx, id)
		if errors.Is(err, domain.ErrVPNUserNotFound) && deleted {
			user = domain.VPNUser{ID: id}
			err = u.Xray.ConfirmUser(ctx, user)
			if err == nil {
				return nil
			}
		} else if err != nil && ctx.Err() == nil {
			return err
		} else if err == nil && !deleted && !user.Pending && !user.DeletePending {
			if err := u.Xray.ConfirmUser(ctx, user); err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("VPN user %d: change saved, confirmation pending; check vpn-user list before retrying: %w", id, domain.ErrVPNSyncPending)
		case <-ticker.C:
		}
	}
}

func (u VPNUsers) Link(ctx context.Context, id int64) (string, error) {
	user, err := u.Store.VPNUser(ctx, id)
	if err != nil {
		return "", err
	}
	if !user.Allowed() {
		return "", fmt.Errorf("VPN user %d: %w", id, domain.ErrVPNUserBlocked)
	}
	if user.Pending {
		return "", domain.ErrVPNSyncPending
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := u.Xray.ConfirmUser(ctx, user); err != nil {
		return "", err
	}
	settings, err := u.Store.ServerSettings(ctx)
	if err != nil {
		return "", err
	}
	return (service.Connections{}).VLESS(user, settings)
}

func (u VPNUsers) QR(ctx context.Context, id int64) ([]byte, error) {
	link, err := u.Link(ctx, id)
	if err != nil {
		return nil, err
	}
	return (service.Connections{}).QR(link)
}

func vpnUserName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", domain.ErrVPNUserName
	}
	return name, nil
}
