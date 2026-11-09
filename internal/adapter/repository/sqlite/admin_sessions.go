package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"proxyk-admin/internal/domain"
)

func (s *Store) Administrator(ctx context.Context) (domain.Administrator, error) {
	var admin domain.Administrator
	err := s.DB.QueryRowContext(ctx, `SELECT username, password_hash, credential_version
		FROM administrators WHERE id = 1`).Scan(&admin.Username, &admin.PasswordHash, &admin.CredentialVersion)
	return admin, err
}

func (s *Store) CreateAdminSession(ctx context.Context, tokenHash []byte, csrfToken string, version int64, now, expires time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A reset may have occurred while the caller was checking bcrypt.
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions (token_hash, csrf_token, credential_version, expires_at)
		SELECT ?, ?, credential_version, ? FROM administrators WHERE id = 1 AND credential_version = ?`,
		tokenHash, csrfToken, expires.Unix(), version)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return domain.ErrAdminSessionInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= ? OR credential_version <> ?`,
		now.Unix(), version); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminSession(ctx context.Context, tokenHash []byte, now time.Time) (domain.AdminSession, error) {
	var session domain.AdminSession
	err := s.DB.QueryRowContext(ctx, `SELECT a.username, s.csrf_token
		FROM admin_sessions s JOIN administrators a ON a.id = 1 AND a.credential_version = s.credential_version
		WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now.Unix()).Scan(&session.Username, &session.CSRFToken)
	if errors.Is(err, sql.ErrNoRows) {
		return session, domain.ErrAdminSessionInvalid
	}
	return session, err
}

func (s *Store) DeleteAdminSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash)
	return err
}
