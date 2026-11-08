package usecase

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

type Bootstrap struct{ Store *sqlite.Store }

func (b Bootstrap) Initialize(ctx context.Context, ip string, port int, credentialsPath string) (domain.ServerSettings, error) {
	tx, err := b.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.ServerSettings{}, err
	}
	defer tx.Rollback()
	var settings domain.ServerSettings
	var saved string
	err = tx.QueryRowContext(ctx, "SELECT settings_json FROM server_settings WHERE id = 1").Scan(&saved)
	if err == nil {
		if err := json.Unmarshal([]byte(saved), &settings); err != nil {
			return settings, err
		}
		if settings.PublicIP != ip || settings.VPNPort != port {
			return settings, fmt.Errorf("existing PUBLIC_IP/VPN_PORT differ; refusing to change connection settings")
		}
		var admin int
		if err := tx.QueryRowContext(ctx, "SELECT id FROM administrators WHERE id = 1").Scan(&admin); err != nil {
			return settings, err
		}
		return settings, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return settings, err
	}
	credentials, hash, err := service.NewCredentials()
	if err != nil {
		return settings, err
	}
	settings, err = service.NewSettings(ip, port)
	if err != nil {
		return settings, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO administrators(id, username, password_hash) VALUES (1, ?, ?)", credentials.Username, hash); err != nil {
		return settings, err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return settings, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO server_settings(id, settings_json) VALUES (1, ?)", string(encoded)); err != nil {
		return settings, err
	}
	encoded, err = json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return settings, err
	}
	// Persist credentials before commit: an interruption after commit must never
	// leave a newly initialized administrator with an unrecoverable password.
	if err = service.WritePrivate(credentialsPath, encoded); err != nil {
		return settings, err
	}
	return settings, tx.Commit()
}

func (b Bootstrap) ResetPassword(ctx context.Context, output string) error {
	tx, err := b.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	credentials, hash, err := service.NewCredentials()
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "SELECT username FROM administrators WHERE id = 1").Scan(&credentials.Username); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE administrators SET password_hash = ?, credential_version = credential_version + 1,
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = 1`, hash)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	if err = service.WritePrivate(output, data); err != nil {
		return err
	}
	return tx.Commit()
}
