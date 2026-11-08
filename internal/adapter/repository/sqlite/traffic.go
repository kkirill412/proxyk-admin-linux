package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"proxyk-admin/internal/domain"
)

func (s *Store) ApplyTraffic(ctx context.Context, batch domain.TrafficBatch) error {
	if batch.ID == "" {
		return fmt.Errorf("missing traffic batch ID")
	}
	if _, err := time.Parse(time.RFC3339Nano, batch.SampledAt); err != nil {
		return fmt.Errorf("invalid traffic sample time")
	}
	for id, traffic := range batch.Users {
		if id <= 0 || traffic.UplinkBytes < 0 || traffic.DownlinkBytes < 0 {
			return fmt.Errorf("invalid traffic sample")
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT last_batch_id FROM traffic_collection WHERE id = 1`).Scan(&previous); err != nil {
		return err
	}
	if previous == batch.ID {
		return tx.Commit()
	}
	for id, traffic := range batch.Users {
		user, err := scanVPNUser(tx.QueryRowContext(ctx, `SELECT `+vpnUserColumns+` FROM vpn_users WHERE id = ?`, id))
		if errors.Is(err, domain.ErrVPNUserNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		user.UplinkBytes = domain.AddTraffic(user.UplinkBytes, traffic.UplinkBytes)
		user.DownlinkBytes = domain.AddTraffic(user.DownlinkBytes, traffic.DownlinkBytes)
		if user.Enabled && !user.Allowed() {
			user.Enabled, user.BlockedReason = false, "limit"
			user.Revision++
		}
		if traffic.UplinkBytes == 0 && traffic.DownlinkBytes == 0 {
			continue
		}
		_, err = tx.ExecContext(ctx, `UPDATE vpn_users SET uplink_bytes = ?, downlink_bytes = ?,
			enabled = ?, blocked_reason = ?, revision = ?, updated_at = ? WHERE id = ?`,
			user.UplinkBytes, user.DownlinkBytes, user.Enabled, user.BlockedReason, user.Revision, batch.SampledAt, id)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE traffic_collection SET last_batch_id = ?, sampled_at = ? WHERE id = 1`, batch.ID, batch.SampledAt); err != nil {
		return err
	}
	return tx.Commit()
}
