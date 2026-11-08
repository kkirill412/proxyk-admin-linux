package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/adapter/xray"
	"proxyk-admin/internal/usecase"
)

const vpnUserUsage = `usage: proxyk-admin vpn-user create NAME
       proxyk-admin vpn-user list
       proxyk-admin vpn-user rename ID NAME
       proxyk-admin vpn-user enable|disable|delete|link|traffic ID`

func runVPNUser(ctx context.Context, store *sqlite.Store, xrayAddr string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(vpnUserUsage)
	}
	users := usecase.VPNUsers{Store: store}
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
		if len(args) != 2 {
			return errors.New(vpnUserUsage)
		}
		user, err := users.Create(ctx, args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, user.ID)
		return err
	case "rename", "enable", "disable", "delete", "link", "traffic":
		return runVPNUserByID(ctx, users, xrayAddr, args, out)
	default:
		return errors.New(vpnUserUsage)
	}
}

func runVPNUserByID(ctx context.Context, users usecase.VPNUsers, xrayAddr string, args []string, out io.Writer) error {
	operation := args[0]
	wantArgs := 2
	if operation == "rename" {
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
		return users.Store.SetVPNUserEnabled(ctx, id, operation == "enable")
	case "delete":
		return users.Store.DeleteVPNUser(ctx, id)
	case "link":
		link, err := users.Link(ctx, id)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, link)
		return err
	case "traffic":
		return printVPNTraffic(ctx, users.Store, xrayAddr, id, out)
	default:
		return errors.New(vpnUserUsage)
	}
}

func printVPNTraffic(ctx context.Context, store *sqlite.Store, address string, id int64, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := store.VPNUser(ctx, id); err != nil {
		return err
	}
	client, err := xray.New(address)
	if err != nil {
		return err
	}
	defer client.Close()
	traffic, err := client.Traffic(ctx, id)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(traffic)
}
