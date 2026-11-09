package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"proxyk-admin/internal/domain"
)

const vpnUserColumns = `id, uuid, name, enabled, traffic_limit_bytes, uplink_bytes,
	downlink_bytes, blocked_reason, created_at, updated_at, revision, synced_revision, delete_pending`
const currentTimestamp = `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`

type rowScanner interface{ Scan(...any) error }

func scanVPNUser(row rowScanner) (domain.VPNUser, error) {
	var u domain.VPNUser
	err := row.Scan(&u.ID, &u.UUID, &u.Name, &u.Enabled, &u.TrafficLimitBytes, &u.UplinkBytes,
		&u.DownlinkBytes, &u.BlockedReason, &u.CreatedAt, &u.UpdatedAt, &u.Revision, &u.SyncedRevision, &u.DeletePending)
	if errors.Is(err, sql.ErrNoRows) {
		return u, domain.ErrVPNUserNotFound
	}
	u.UpdateView()
	return u, err
}

func (s *Store) CreateVPNUser(ctx context.Context, user domain.VPNUser) (domain.VPNUser, error) {
	if user.TrafficLimitBytes != nil && *user.TrafficLimitBytes == 0 {
		user.Enabled, user.BlockedReason = false, "limit"
	}
	return scanVPNUser(s.DB.QueryRowContext(ctx, `INSERT INTO vpn_users
		(uuid, name, enabled, traffic_limit_bytes, blocked_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, `+currentTimestamp+`, `+currentTimestamp+`) RETURNING `+vpnUserColumns,
		user.UUID, user.Name, user.Enabled, user.TrafficLimitBytes, user.BlockedReason))
}

func (s *Store) VPNUser(ctx context.Context, id int64) (domain.VPNUser, error) {
	return scanVPNUser(s.DB.QueryRowContext(ctx, `SELECT `+vpnUserColumns+` FROM vpn_users WHERE id = ?`, id))
}

func (s *Store) ListVPNUsers(ctx context.Context) ([]domain.VPNUser, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+vpnUserColumns+` FROM vpn_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []domain.VPNUser{}
	for rows.Next() {
		user, err := scanVPNUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) RenameVPNUser(ctx context.Context, id int64, name string) error {
	return requireVPNUser(s.DB.ExecContext(ctx, `UPDATE vpn_users SET name = ?, updated_at = `+currentTimestamp+`
		WHERE id = ? AND delete_pending = 0`, name, id))
}

func (s *Store) SetVPNUserEnabled(ctx context.Context, id int64, enabled bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE vpn_users SET enabled = ?,
		blocked_reason = CASE WHEN ? THEN '' ELSE 'manual' END,
		revision = revision + 1, updated_at = `+currentTimestamp+`
		WHERE id = ? AND delete_pending = 0 AND
		(NOT ? OR traffic_limit_bytes IS NULL OR uplink_bytes < traffic_limit_bytes - downlink_bytes)`, enabled, enabled, id, enabled)
	err = requireVPNUser(result, err)
	if errors.Is(err, domain.ErrVPNUserNotFound) && enabled {
		user, readErr := s.VPNUser(ctx, id)
		if readErr == nil && !user.DeletePending {
			return domain.ErrTrafficLimit
		}
	}
	return err
}

func (s *Store) SetVPNUserLimit(ctx context.Context, id int64, limit *int64) error {
	if limit != nil && *limit < 0 {
		return domain.ErrInvalidTrafficLimit
	}
	return requireVPNUser(s.DB.ExecContext(ctx, `UPDATE vpn_users SET traffic_limit_bytes = ?,
		enabled = CASE WHEN ? IS NOT NULL AND uplink_bytes >= ? - downlink_bytes THEN 0 ELSE enabled END,
		blocked_reason = CASE WHEN ? IS NOT NULL AND uplink_bytes >= ? - downlink_bytes THEN 'limit' ELSE blocked_reason END,
		revision = revision + 1, updated_at = `+currentTimestamp+` WHERE id = ? AND delete_pending = 0`,
		limit, limit, limit, limit, limit, id))
}

// Keep a tombstone until Xray has confirmed removal; retries survive restarts.
func (s *Store) DeleteVPNUser(ctx context.Context, id int64) error {
	return requireVPNUser(s.DB.ExecContext(ctx, `UPDATE vpn_users SET delete_pending = 1,
		enabled = 0, revision = revision + 1, updated_at = `+currentTimestamp+` WHERE id = ?`, id))
}

// Acknowledge only the exact snapshot applied to Xray. A concurrent CLI edit must
// remain pending, even if it changed the row while the gRPC request was in flight.
func (s *Store) ConfirmVPNUsers(ctx context.Context, users []domain.VPNUser) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, user := range users {
		if user.DeletePending {
			_, err = tx.ExecContext(ctx, `DELETE FROM vpn_users WHERE id = ? AND revision = ? AND delete_pending = 1`, user.ID, user.Revision)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE vpn_users SET synced_revision = ? WHERE id = ? AND revision = ?`, user.Revision, user.ID, user.Revision)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func requireVPNUser(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 0 {
		return domain.ErrVPNUserNotFound
	}
	return err
}

func (s *Store) ServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	var settings domain.ServerSettings
	var stored string
	if err := s.DB.QueryRowContext(ctx, `SELECT settings_json FROM server_settings WHERE id = 1`).Scan(&stored); err != nil {
		return settings, err
	}
	err := json.Unmarshal([]byte(stored), &settings)
	return settings, err
}
