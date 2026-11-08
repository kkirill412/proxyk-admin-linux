package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
	"proxyk-admin/internal/migration"
)

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := u.Query()
	for _, pragma := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "foreign_keys(ON)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// A single writer per process; BEGIN IMMEDIATE and busy_timeout also protect
	// against concurrent CLI processes accessing the same WAL database.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = migration.Apply(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Check(ctx context.Context) error {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM administrators) + (SELECT count(*) FROM server_settings)`).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("initialization required")
	}
	return nil
}
