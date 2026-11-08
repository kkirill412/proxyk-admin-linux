package domain

import (
	"errors"
	"math"
)

const VLESSFlow = "xtls-rprx-vision"

var ErrVPNUserNotFound = errors.New("VPN user not found")
var ErrTrafficLimit = errors.New("traffic limit exhausted; increase the limit before enabling the user")
var ErrVPNSyncPending = errors.New("VPN changes have not been confirmed by Xray")

type VPNUser struct {
	ID                int64  `json:"id"`
	UUID              string `json:"-"` // VLESS credential, exposed only in the connection link.
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
	UplinkBytes       int64  `json:"uplink_bytes"`
	DownlinkBytes     int64  `json:"downlink_bytes"`
	BlockedReason     string `json:"blocked_reason"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Revision          int64  `json:"-"`
	SyncedRevision    int64  `json:"-"`
	DeletePending     bool   `json:"delete_pending"`
	Status            string `json:"status"`
	Pending           bool   `json:"pending"`
	TotalBytes        int64  `json:"total_bytes"`
	RemainingBytes    *int64 `json:"remaining_bytes"`
}

func (u VPNUser) Allowed() bool {
	return u.Enabled && !u.DeletePending && (u.TrafficLimitBytes == nil || AddTraffic(u.UplinkBytes, u.DownlinkBytes) < *u.TrafficLimitBytes)
}

func (u *VPNUser) UpdateView() {
	u.Status = "blocked"
	if u.Allowed() {
		u.Status = "active"
	}
	u.Pending = u.Revision != u.SyncedRevision
	u.TotalBytes = AddTraffic(u.UplinkBytes, u.DownlinkBytes)
	u.RemainingBytes = nil
	if u.TrafficLimitBytes != nil {
		remaining := max(int64(0), *u.TrafficLimitBytes-u.TotalBytes)
		u.RemainingBytes = &remaining
	}
}

// Saturation keeps malicious or exceptionally large counters from wrapping.
func AddTraffic(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

type VPNTraffic struct {
	UplinkBytes   int64 `json:"uplink_bytes"`
	DownlinkBytes int64 `json:"downlink_bytes"`
}

type TrafficBatch struct {
	ID        string               `json:"id"`
	SampledAt string               `json:"sampled_at"`
	Users     map[int64]VPNTraffic `json:"users"`
}
