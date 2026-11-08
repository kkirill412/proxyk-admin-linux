package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

type trafficSource struct {
	calls  int
	userID int64
	fail   bool
}

func (s *trafficSource) DrainTraffic(context.Context) (map[int64]domain.VPNTraffic, error) {
	s.calls++
	if s.fail {
		return nil, errors.New("xray down")
	}
	return map[int64]domain.VPNTraffic{s.userID: {UplinkBytes: 10, DownlinkBytes: 20}}, nil
}

type failingTrafficStore struct {
	TrafficStore
	fail bool
}

func (s *failingTrafficStore) ApplyTraffic(ctx context.Context, b domain.TrafficBatch) error {
	if s.fail {
		return errors.New("database unavailable")
	}
	return s.TrafficStore.ApplyTraffic(ctx, b)
}

func TestCollectorRetriesBeforeResetAndRecoversJournal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	user, err := store.CreateVPNUser(ctx, domain.VPNUser{UUID: "test", Name: "Test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	source := &trafficSource{userID: user.ID}
	database := &failingTrafficStore{TrafficStore: store, fail: true}
	journal := filepath.Join(dir, "traffic.json")
	collector := &TrafficCollector{Store: database, Xray: source, JournalPath: journal}
	for range 2 {
		if err := collector.Collect(ctx); err == nil {
			t.Fatal("missing database failure")
		}
	}
	if source.calls != 1 {
		t.Fatal("counters reset again before durable application")
	}
	info, err := os.Stat(journal)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal permissions: %v", err)
	}
	// Simulate process death with an unapplied, durable batch and Xray down.
	source.fail = true
	restarted := &TrafficCollector{Store: store, Xray: source, JournalPath: journal}
	if err := restarted.Collect(ctx); err == nil {
		t.Fatal("missing Xray failure")
	}
	saved, _ := store.VPNUser(ctx, user.ID)
	if saved.TotalBytes != 30 {
		t.Fatalf("pending batch lost: %d", saved.TotalBytes)
	}
	// Another restart replays the same journal, without charging it twice.
	restarted = &TrafficCollector{Store: store, Xray: source, JournalPath: journal}
	_ = restarted.Collect(ctx)
	saved, _ = store.VPNUser(ctx, user.ID)
	if saved.TotalBytes != 30 {
		t.Fatal("replayed journal counted twice")
	}
	source.fail = false
	if err := restarted.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	saved, _ = store.VPNUser(ctx, user.ID)
	if saved.TotalBytes != 60 {
		t.Fatal("new batch not accumulated")
	}
}

func TestCollectorRejectsCorruptJournalAndMultipleOwners(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "traffic.json")
	if err := os.WriteFile(journal, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	source := &trafficSource{}
	collector := TrafficCollector{Xray: source, JournalPath: journal}
	if err := collector.Collect(context.Background()); err == nil || source.calls != 0 {
		t.Fatal("corrupt journal reset counters")
	}
	lock, err := service.CollectorLock(filepath.Join(dir, "traffic.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	second, err := service.CollectorLock(filepath.Join(dir, "traffic.lock"))
	if err == nil {
		second.Close()
		t.Fatal("second collector acquired the same lock")
	}
}
