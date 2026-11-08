package usecase

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
)

type userRuntime struct {
	fail  bool
	users []domain.VPNUser
}

func (r *userRuntime) Check(context.Context) error { return nil }
func (r *userRuntime) SyncUsers(_ context.Context, users []domain.VPNUser) error {
	if r.fail {
		return errors.New("Xray unavailable")
	}
	r.users = users
	return nil
}
func (r *userRuntime) ConfirmUser(context.Context, domain.VPNUser) error {
	if r.fail {
		return errors.New("Xray account missing")
	}
	return nil
}

func TestPendingCreateDoesNotExportUnconfirmedConnection(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if _, err := store.DB.Exec(`INSERT INTO server_settings(id,settings_json) VALUES (1,'{"public_ip":"8.8.8.8","vpn_port":443}')`); err != nil {
		t.Fatal(err)
	}
	runtime := &userRuntime{}
	users := VPNUsers{Store: store, Xray: runtime}
	for _, name := range []string{"", "  ", "invalid\nname", strings.Repeat("a", 81)} {
		if _, err := users.Create(ctx, name, nil); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	user, err := users.Create(deadline, " Laptop ", nil)
	if !errors.Is(err, domain.ErrVPNSyncPending) || user.ID == 0 || user.UUID == "" || user.Name != "Laptop" {
		t.Fatalf("failed create lost pending user: %+v %v", user, err)
	}
	if link, err := users.Link(ctx, user.ID); err == nil || link != "" {
		t.Fatal("pending user exported")
	}
	if err := store.ConfirmVPNUsers(ctx, []domain.VPNUser{user}); err != nil {
		t.Fatal(err)
	}
	runtime.fail = true
	if link, err := users.Link(ctx, user.ID); err == nil || link != "" {
		t.Fatal("stale database acknowledgement exported a missing runtime account")
	}
	runtime.fail = false
	if err := users.Wait(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}
	if link, err := users.Link(ctx, user.ID); err != nil || !strings.HasPrefix(link, "vless://") {
		t.Fatalf("confirmed connection not available: %v", err)
	}
}

func TestSyncRetriesWithoutAcknowledgingFailedRuntimeChanges(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	user, err := store.CreateVPNUser(ctx, domain.VPNUser{Name: "Test", UUID: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &userRuntime{fail: true}
	database := &failingTrafficStore{TrafficStore: store}
	collector := &TrafficCollector{Store: database, Xray: &trafficSource{userID: user.ID}, JournalPath: filepath.Join(dir, "traffic.json")}
	sync := VPNUserSync{Store: store, Xray: runtime, Traffic: collector}
	if err := sync.sync(ctx); err == nil {
		t.Fatal("runtime failure ignored")
	}
	pending, _ := store.VPNUser(ctx, user.ID)
	if !pending.Pending {
		t.Fatal("runtime failure acknowledged")
	}
	runtime.fail = false
	if err := sync.sync(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ = store.VPNUser(ctx, user.ID)
	if pending.Pending {
		t.Fatal("successful retry not acknowledged")
	}
	if err := store.SetVPNUserEnabled(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}
	database.fail = true
	if err := sync.sync(ctx); err == nil {
		t.Fatal("accounting failure ignored")
	}
	if len(runtime.users) != 1 || runtime.users[0].Allowed() {
		t.Fatal("accounting failure prevented explicit revocation")
	}
}
