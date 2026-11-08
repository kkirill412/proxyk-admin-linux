package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/migration"
)

func TestDatabasePragmasMigrationsAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	first, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.DB.Close()
	second, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.DB.Close()
	for name, expected := range map[string]string{"journal_mode": "wal", "busy_timeout": "10000", "foreign_keys": "1", "synchronous": "2"} {
		var value string
		if err := second.DB.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != expected {
			t.Fatalf("%s = %s, want %s", name, value, expected)
		}
	}
	if err := migration.Apply(ctx, first.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := first.DB.ExecContext(ctx, "CREATE TABLE counter (value INTEGER); INSERT INTO counter VALUES (0)"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Go(func() {
			store := first
			if i%2 == 0 {
				store = second
			}
			tx, err := store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer tx.Rollback()
			var value int
			if err = tx.QueryRowContext(ctx, "SELECT value FROM counter").Scan(&value); err != nil {
				t.Error(err)
				return
			}
			if _, err = tx.ExecContext(ctx, "UPDATE counter SET value = ?", value+1); err != nil {
				t.Error(err)
				return
			}
			if err = tx.Commit(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var value int
	if err := second.DB.QueryRowContext(ctx, "SELECT value FROM counter").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 30 {
		t.Fatalf("lost writes: %d", value)
	}
	if _, err := first.DB.ExecContext(ctx, "UPDATE schema_migrations SET checksum = 'modified'"); err != nil {
		t.Fatal(err)
	}
	if err := migration.Apply(ctx, first.DB); err == nil {
		t.Fatal("changed migration accepted")
	}
}

func TestRefuseNewerSchema(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if _, err := store.DB.Exec("INSERT INTO schema_migrations(version, checksum) VALUES ('9999_future.sql', 'x')"); err != nil {
		t.Fatal(err)
	}
	if err := migration.Apply(context.Background(), store.DB); err == nil {
		t.Fatal("downgrade should fail")
	}
}
