package usecase

import (
	"context"
	"time"
)

type Checker interface{ Check(context.Context) error }

type Health struct{ Database, Xray Checker }
type HealthStatus struct {
	Status   string `json:"status"`
	Database bool   `json:"database"`
	Xray     bool   `json:"xray"`
}

func (h Health) Readiness(ctx context.Context) HealthStatus {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status := HealthStatus{Status: "unavailable"}
	status.Database = h.Database.Check(ctx) == nil
	status.Xray = h.Xray.Check(ctx) == nil
	if status.Database && status.Xray {
		status.Status = "ready"
	}
	return status
}
