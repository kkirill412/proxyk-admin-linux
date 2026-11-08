package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/adapter/xray"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
	"proxyk-admin/internal/usecase"
)

const vpnUserUsage = `usage: proxyk-admin vpn-user create NAME [LIMIT_BYTES|unlimited]
       proxyk-admin vpn-user list
       proxyk-admin vpn-user rename ID NAME
       proxyk-admin vpn-user limit ID LIMIT_BYTES|unlimited
       proxyk-admin vpn-user qr ID OUTPUT.png
       proxyk-admin vpn-user enable|disable|delete|link|traffic ID`

func runVPNUser(ctx context.Context, store *sqlite.Store, xrayAddr string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(vpnUserUsage)
	}
	client, err := xray.New(xrayAddr)
	if err != nil {
		return err
	}
	defer client.Close()
	users := usecase.VPNUsers{Store: store, Xray: client}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New(vpnUserUsage)
		}
		list, err := store.ListVPNUsers(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(list)
	case "create":
		if len(args) < 2 || len(args) > 3 {
			return errors.New(vpnUserUsage)
		}
		var limit *int64
		if len(args) == 3 {
			limit, err = parseTrafficLimit(args[2])
			if err != nil {
				return err
			}
		}
		user, err := users.Create(ctx, args[1], limit)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, user.ID)
		return err
	case "rename", "enable", "disable", "delete", "link", "traffic", "limit", "qr":
		return runVPNUserByID(ctx, users, args, out)
	default:
		return errors.New(vpnUserUsage)
	}
}

func runVPNUserByID(ctx context.Context, users usecase.VPNUsers, args []string, out io.Writer) error {
	operation := args[0]
	wantArgs := 2
	if operation == "rename" || operation == "limit" || operation == "qr" {
		wantArgs = 3
	}
	if len(args) != wantArgs {
		return errors.New(vpnUserUsage)
	}
	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || id < 1 {
		return errors.New("VPN user ID must be a positive integer")
	}
	switch operation {
	case "rename":
		return users.Rename(ctx, id, args[2])
	case "enable", "disable":
		return users.SetEnabled(ctx, id, operation == "enable")
	case "delete":
		return users.Delete(ctx, id)
	case "limit":
		limit, err := parseTrafficLimit(args[2])
		if err != nil {
			return err
		}
		return users.SetLimit(ctx, id, limit)
	case "link":
		link, err := users.Link(ctx, id)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, link)
		return err
	case "qr":
		data, err := users.QR(ctx, id)
		if err != nil {
			return err
		}
		return service.WritePrivate(args[2], data)
	case "traffic":
		user, err := users.Store.VPNUser(ctx, id)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(domain.VPNTraffic{UplinkBytes: user.UplinkBytes, DownlinkBytes: user.DownlinkBytes})
	default:
		return errors.New(vpnUserUsage)
	}
}

func parseTrafficLimit(value string) (*int64, error) {
	if value == "unlimited" {
		return nil, nil
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit < 0 {
		return nil, errors.New("traffic limit must be a non-negative byte count or unlimited")
	}
	return &limit, nil
}
