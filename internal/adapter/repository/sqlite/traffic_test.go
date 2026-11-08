package sqlite_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
)

func trafficStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB.Close() })
	return store
}

func TestTrafficQuotaAndReplay(t *testing.T) {
	ctx := context.Background()
	store := trafficStore(t)
	limit := int64(100)
	user, err := store.CreateVPNUser(ctx, domain.VPNUser{UUID: "test", Name: "Limited", Enabled: true, TrafficLimitBytes: &limit})
	if err != nil {
		t.Fatal(err)
	}
	batch := domain.TrafficBatch{ID: "batch-1", SampledAt: time.Now().UTC().Format(time.RFC3339Nano), Users: map[int64]domain.VPNTraffic{user.ID: {UplinkBytes: 40, DownlinkBytes: 60}}}
	for range 2 {
		if err := store.ApplyTraffic(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	blocked, err := store.VPNUser(ctx, user.ID)
	if err != nil || blocked.Enabled || blocked.BlockedReason != "limit" || blocked.TotalBytes != 100 || *blocked.RemainingBytes != 0 {
		t.Fatalf("bad quota/replay result: %+v, %v", blocked, err)
	}
	if err := store.SetVPNUserEnabled(ctx, user.ID, true); !errors.Is(err, domain.ErrTrafficLimit) {
		t.Fatalf("exhausted user enabled: %v", err)
	}
	// A sync started before quota exhaustion cannot acknowledge the later block.
	if err := store.ConfirmVPNUsers(ctx, []domain.VPNUser{user}); err != nil {
		t.Fatal(err)
	}
	pending, _ := store.VPNUser(ctx, user.ID)
	if !pending.Pending {
		t.Fatal("stale snapshot acknowledged a concurrent block")
	}
	limit = 200
	if err := store.SetVPNUserLimit(ctx, user.ID, &limit); err != nil {
		t.Fatal(err)
	}
	blocked, _ = store.VPNUser(ctx, user.ID)
	if blocked.Enabled {
		t.Fatal("raising quota automatically enabled user")
	}
	if err := store.SetVPNUserEnabled(ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	// Lowering below already persisted consumption immediately blocks the user.
	limit = 99
	if err := store.SetVPNUserLimit(ctx, user.ID, &limit); err != nil {
		t.Fatal(err)
	}
	blocked, _ = store.VPNUser(ctx, user.ID)
	if blocked.Allowed() || blocked.BlockedReason != "limit" {
		t.Fatal("lowering quota failed to block")
	}
	if err := store.SetVPNUserLimit(ctx, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVPNUserEnabled(ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	batch.ID = "batch-2"
	batch.Users[user.ID] = domain.VPNTraffic{UplinkBytes: math.MaxInt64, DownlinkBytes: math.MaxInt64}
	if err := store.ApplyTraffic(ctx, batch); err != nil {
		t.Fatal(err)
	}
	unlimited, _ := store.VPNUser(ctx, user.ID)
	if !unlimited.Allowed() || unlimited.TotalBytes != math.MaxInt64 || unlimited.RemainingBytes != nil {
		t.Fatalf("unlimited/overflow handling: %+v", unlimited)
	}
}

func TestZeroLimitAndInvalidBatch(t *testing.T) {
	ctx := context.Background()
	store := trafficStore(t)
	zero := int64(0)
	user, err := store.CreateVPNUser(ctx, domain.VPNUser{UUID: "zero", Name: "Zero", Enabled: true, TrafficLimitBytes: &zero})
	if err != nil || user.Allowed() || user.BlockedReason != "limit" {
		t.Fatalf("zero quota: %+v %v", user, err)
	}
	bad := domain.TrafficBatch{ID: "bad", SampledAt: time.Now().Format(time.RFC3339Nano), Users: map[int64]domain.VPNTraffic{user.ID: {UplinkBytes: 5}, 999: {DownlinkBytes: -1}}}
	if err := store.ApplyTraffic(ctx, bad); err == nil {
		t.Fatal("negative sample accepted")
	}
	unchanged, _ := store.VPNUser(ctx, user.ID)
	if unchanged.TotalBytes != 0 {
		t.Fatal("invalid batch was partially applied")
	}
	if err := store.DeleteVPNUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmVPNUsers(ctx, []domain.VPNUser{user}); err != nil {
		t.Fatal(err)
	}
	pending, _ := store.VPNUser(ctx, user.ID)
	if !pending.DeletePending || !pending.Pending {
		t.Fatal("stale sync removed pending deletion")
	}
}
