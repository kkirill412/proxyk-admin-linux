package domain

import "errors"

const VLESSFlow = "xtls-rprx-vision"

var ErrVPNUserNotFound = errors.New("VPN user not found")

type VPNUser struct {
	ID      int64  `json:"id"`
	UUID    string `json:"-"` // VLESS credential, exposed only in the connection link.
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type VPNTraffic struct {
	UplinkBytes   int64 `json:"uplink_bytes"`
	DownlinkBytes int64 `json:"downlink_bytes"`
}
