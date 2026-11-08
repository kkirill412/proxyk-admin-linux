package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

type TrafficReader interface {
	DrainTraffic(context.Context) (map[int64]domain.VPNTraffic, error)
}
type TrafficStore interface {
	ApplyTraffic(context.Context, domain.TrafficBatch) error
}

// TrafficCollector has one owner (the locked server process), not one owner per
// HTTP request or CLI. A failed database write is retried before reading Xray again.
type TrafficCollector struct {
	Store       TrafficStore
	Xray        TrafficReader
	JournalPath string
	pending     *domain.TrafficBatch
	loaded      bool
}

func (c *TrafficCollector) Collect(ctx context.Context) error {
	if !c.loaded {
		data, err := os.ReadFile(c.JournalPath)
		if err == nil {
			var batch domain.TrafficBatch
			if err := json.Unmarshal(data, &batch); err != nil {
				return fmt.Errorf("invalid pending traffic journal: %w", err)
			}
			c.pending = &batch
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		c.loaded = true
	}
	if c.pending != nil {
		if err := c.savePending(ctx); err != nil {
			return err
		}
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	traffic, err := c.Xray.DrainTraffic(ctx)
	if err != nil {
		return err
	}
	c.pending = &domain.TrafficBatch{ID: id.String(), SampledAt: time.Now().UTC().Format(time.RFC3339Nano), Users: traffic}
	return c.savePending(ctx)
}

func (c *TrafficCollector) savePending(ctx context.Context) error {
	data, err := json.Marshal(c.pending)
	if err != nil {
		return err
	}
	if err = service.WritePrivate(c.JournalPath, data); err != nil {
		return err
	}
	if err = c.Store.ApplyTraffic(ctx, *c.pending); err != nil {
		return err
	}
	// Keep the last journal on disk: replay after a crash is harmless because
	// ApplyTraffic commits the batch ID and all user totals in one transaction.
	c.pending = nil
	return nil
}
