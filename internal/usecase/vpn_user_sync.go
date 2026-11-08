package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"proxyk-admin/internal/domain"
)

type VPNUserSyncStore interface {
	ListVPNUsers(context.Context) ([]domain.VPNUser, error)
	ConfirmVPNUsers(context.Context, []domain.VPNUser) error
}

type VPNUserRuntime interface {
	Checker
	SyncUsers(context.Context, []domain.VPNUser) error
}

// VPNUserSync is the sole writer to Xray. CLI operations commit desired state to
// SQLite; each pass converges the running inbound, including after Xray restarts.
type VPNUserSync struct {
	Store   VPNUserSyncStore
	Xray    VPNUserRuntime
	Traffic *TrafficCollector

	synced atomic.Bool
}

func (s *VPNUserSync) Check(ctx context.Context) error {
	if !s.synced.Load() {
		return errors.New("VPN user synchronization pending")
	}
	return s.Xray.Check(ctx)
}

func (s *VPNUserSync) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var failed bool
	for ctx.Err() == nil {
		err := s.sync(ctx)
		if ctx.Err() != nil {
			return
		}
		s.synced.Store(err == nil)
		if err != nil && !failed {
			slog.Warn("VPN user synchronization failed", "error", err)
		} else if err == nil && failed {
			slog.Info("VPN user synchronization recovered")
		}
		failed = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *VPNUserSync) sync(ctx context.Context) error {
	// Even if accounting fails, apply explicit blocks and removals using the
	// last durable totals. Readiness still reports the accounting failure.
	collectCtx, cancelCollect := context.WithTimeout(ctx, 3*time.Second)
	trafficErr := s.Traffic.Collect(collectCtx)
	cancelCollect()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	users, err := s.Store.ListVPNUsers(ctx)
	if err != nil {
		return fmt.Errorf("read VPN users: %w", err)
	}
	if err := s.Xray.SyncUsers(ctx, users); err != nil {
		return err
	}
	if err := s.Store.ConfirmVPNUsers(ctx, users); err != nil {
		return err
	}
	return trafficErr
}
