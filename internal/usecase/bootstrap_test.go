package usecase_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/usecase"
)

func TestBootstrapPersistenceConcurrentInitializationAndReset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store, err := sqlite.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	b := usecase.Bootstrap{Store: store}
	settings, err := b.Initialize(ctx, "8.8.8.8", 443, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var credentials domain.Credentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		t.Fatal(err)
	}
	if len(credentials.Password) < 20 {
		t.Fatal("password too short")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials are not private")
	}
	var hash string
	if err := store.DB.QueryRow("SELECT password_hash FROM administrators").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(credentials.Password)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			restored, err := b.Initialize(ctx, "8.8.8.8", 443, path)
			if err != nil {
				t.Error(err)
				return
			}
			if restored != settings {
				t.Error("settings rotated on reinstall")
			}
		})
	}
	wg.Wait()
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("credentials overwritten")
	}
	if _, err := b.Initialize(ctx, "8.8.4.4", 443, path); err == nil {
		t.Fatal("IP silently changed")
	}
	if err := b.ResetPassword(ctx, path); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reset domain.Credentials
	if err := json.Unmarshal(after, &reset); err != nil {
		t.Fatal(err)
	}
	if reset.Username != credentials.Username || reset.Password == credentials.Password {
		t.Fatal("bad password reset")
	}
	if err := store.DB.QueryRow("SELECT password_hash FROM administrators").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(reset.Password)); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(credentials.Password)); err == nil {
		t.Fatal("old password still valid")
	}
	var version int
	if err := store.DB.QueryRow("SELECT credential_version FROM administrators").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatal("credential version not increased")
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.DB.Close()
	final, err := (usecase.Bootstrap{Store: reopened}).Initialize(ctx, "8.8.8.8", 443, path)
	if err != nil || final != settings {
		t.Fatal("restart lost settings", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reinstall recreated credentials")
	}
}

func TestCredentialWriteFailureRollsBackDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	b := usecase.Bootstrap{Store: store}
	if _, err := b.Initialize(ctx, "8.8.8.8", 443, dir); err == nil {
		t.Fatal("directory cannot be credential file")
	}
	if err := store.Check(ctx); err == nil {
		t.Fatal("partial bootstrap committed")
	}
	if _, err := b.Initialize(ctx, "8.8.8.8", 443, filepath.Join(dir, "credentials.json")); err != nil {
		t.Fatal(err)
	}
}
