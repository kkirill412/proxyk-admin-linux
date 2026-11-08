package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"proxyk-admin/internal/domain"
)

func (s *Store) CreateVPNUser(ctx context.Context, user domain.VPNUser) (domain.VPNUser, error) {
	err := s.DB.QueryRowContext(ctx, `INSERT INTO vpn_users (uuid, name, enabled)
		VALUES (?, ?, ?) RETURNING id`, user.UUID, user.Name, user.Enabled).Scan(&user.ID)
	if err != nil {
		return domain.VPNUser{}, err
	}
	return user, nil
}

func (s *Store) VPNUser(ctx context.Context, id int64) (domain.VPNUser, error) {
	var user domain.VPNUser
	err := s.DB.QueryRowContext(ctx, `SELECT id, uuid, name, enabled FROM vpn_users WHERE id = ?`, id).
		Scan(&user.ID, &user.UUID, &user.Name, &user.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.VPNUser{}, domain.ErrVPNUserNotFound
	}
	return user, err
}

func (s *Store) ListVPNUsers(ctx context.Context) ([]domain.VPNUser, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, uuid, name, enabled FROM vpn_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []domain.VPNUser{}
	for rows.Next() {
		var user domain.VPNUser
		if err := rows.Scan(&user.ID, &user.UUID, &user.Name, &user.Enabled); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) RenameVPNUser(ctx context.Context, id int64, name string) error {
	return requireVPNUser(s.DB.ExecContext(ctx, `UPDATE vpn_users SET name = ? WHERE id = ?`, name, id))
}

func (s *Store) SetVPNUserEnabled(ctx context.Context, id int64, enabled bool) error {
	return requireVPNUser(s.DB.ExecContext(ctx, `UPDATE vpn_users SET enabled = ? WHERE id = ?`, enabled, id))
}

func (s *Store) DeleteVPNUser(ctx context.Context, id int64) error {
	return requireVPNUser(s.DB.ExecContext(ctx, `DELETE FROM vpn_users WHERE id = ?`, id))
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
