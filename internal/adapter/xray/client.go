package xray

import (
	"context"

	"proxyk-admin/internal/domain"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	connection *grpc.ClientConn
	stats      stats.StatsServiceClient
	handlers   handler.HandlerServiceClient
}

func New(address string) (*Client, error) {
	// This endpoint is intentionally confined to the private Docker network.
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		connection: connection,
		stats:      stats.NewStatsServiceClient(connection),
		handlers:   handler.NewHandlerServiceClient(connection),
	}, nil
}

func (c *Client) Check(ctx context.Context) error {
	_, err := c.stats.GetSysStats(ctx, &stats.SysStatsRequest{})
	return err
}

// Traffic reads Xray's process-local counters without resetting them.
func (c *Client) Traffic(ctx context.Context, id int64) (domain.VPNTraffic, error) {
	var traffic domain.VPNTraffic
	prefix := "user>>>" + vpnUserEmail(id) + ">>>traffic>>>"
	response, err := c.stats.QueryStats(ctx, &stats.QueryStatsRequest{Pattern: prefix})
	if err != nil {
		return traffic, rpcError("read user traffic", err)
	}
	for _, counter := range response.GetStat() {
		switch counter.GetName() {
		case prefix + "uplink":
			traffic.UplinkBytes = counter.GetValue()
		case prefix + "downlink":
			traffic.DownlinkBytes = counter.GetValue()
		}
	}
	return traffic, nil
}

func (c *Client) Close() error { return c.connection.Close() }
