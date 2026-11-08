package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
)

func TestVPNUsersOnStageOneDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	seedStageOne(t, path)
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	settings, err := store.ServerSettings(ctx)
	if err != nil || settings.PublicIP != "8.8.8.8" || settings.ShortID != "1234abcd" {
		t.Fatalf("stage 1 settings were lost or changed: %v", err)
	}
	var passwordHash string
	if err := store.DB.QueryRowContext(ctx, "SELECT password_hash FROM administrators WHERE id = 1").Scan(&passwordHash); err != nil || passwordHash != "existing-hash" {
		t.Fatalf("stage 1 administrator was lost or changed: %v", err)
	}
	user, err := store.CreateVPNUser(ctx, domain.VPNUser{
		UUID: "85e34f1f-7c1c-4147-a0aa-e4c8777e0492", Name: "Laptop", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateVPNUser(ctx, user); err == nil {
		t.Fatal("duplicate VPN credential accepted")
	}
	if err := store.RenameVPNUser(ctx, user.ID, "Travel laptop"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.SetVPNUserEnabled(ctx, user.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.DB.Close()
	users, err := reopened.ListVPNUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	user.Name, user.Enabled = "Travel laptop", false
	if len(users) != 1 || users[0] != user {
		t.Fatal("stored user did not survive reopen")
	}
	encoded, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), user.UUID) {
		t.Fatal("public user metadata exposed the VPN credential")
	}
	if err := reopened.DeleteVPNUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.VPNUser(ctx, user.ID); !errors.Is(err, domain.ErrVPNUserNotFound) {
		t.Fatalf("expected missing user after delete, got %v", err)
	}
	newUser, err := reopened.CreateVPNUser(ctx, domain.VPNUser{
		UUID: "1b214a31-776c-40e0-95a1-1dd71d57f4a0", Name: "Tablet", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if newUser.ID <= user.ID {
		t.Fatal("deleted user ID was reused")
	}
}

func seedStageOne(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migration", "0001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY, checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));
		INSERT INTO administrators (id, username, password_hash) VALUES (1, 'existing-admin', 'existing-hash');
		INSERT INTO server_settings (id, settings_json) VALUES (1, '{"public_ip":"8.8.8.8","short_id":"1234abcd"}')`); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(body))
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, checksum) VALUES ('0001_initial.sql', ?)`, checksum); err != nil {
		t.Fatal(err)
	}
}
