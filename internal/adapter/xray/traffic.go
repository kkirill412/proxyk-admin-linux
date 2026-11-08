package xray

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	stats "github.com/xtls/xray-core/app/stats/command"
	"proxyk-admin/internal/domain"
)

// Atomic counter swaps ensure that a byte can only belong to one returned batch.
// Only the server's collector may call this method. Foreign accounts are untouched.
func (c *Client) DrainTraffic(ctx context.Context) (map[int64]domain.VPNTraffic, error) {
	response, err := c.stats.QueryStats(ctx, &stats.QueryStatsRequest{Pattern: "user>>>" + userEmailPrefix, Reset_: true})
	if err != nil {
		return nil, rpcError("collect traffic", err)
	}
	result := make(map[int64]domain.VPNTraffic)
	for _, counter := range response.GetStat() {
		parts := strings.Split(counter.GetName(), ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" || !managedEmail(parts[1]) {
			continue
		}
		id, _ := strconv.ParseInt(strings.TrimPrefix(parts[1], userEmailPrefix), 10, 64)
		if counter.GetValue() < 0 {
			return nil, fmt.Errorf("xray: negative traffic counter")
		}
		traffic := result[id]
		switch parts[3] {
		case "uplink":
			traffic.UplinkBytes = counter.GetValue()
		case "downlink":
			traffic.DownlinkBytes = counter.GetValue()
		default:
			continue
		}
		result[id] = traffic
	}
	return result, nil
}

// ConfirmUser verifies the live runtime, not just an old SQLite acknowledgement.
func (c *Client) ConfirmUser(ctx context.Context, user domain.VPNUser) error {
	wanted, err := desiredUsers([]domain.VPNUser{user})
	if err != nil {
		return err
	}
	current, err := c.listInboundUsers(ctx)
	if err != nil {
		return err
	}
	for _, live := range current {
		if !strings.EqualFold(live.email, vpnUserEmail(user.ID)) {
			continue
		}
		expected, ok := wanted[live.email]
		if ok && live.level == 0 && live.account.GetId() == expected.account.GetId() && live.account.GetFlow() == domain.VLESSFlow {
			return nil
		}
		return domain.ErrVPNSyncPending
	}
	if !user.Allowed() {
		return nil
	}
	return domain.ErrVPNSyncPending
}
