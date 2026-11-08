package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/adapter/xray"
	"proxyk-admin/internal/config"
	"proxyk-admin/internal/usecase"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		return healthcheck(ctx, cfg)
	}
	if command != "serve" && command != "init" && command != "reset-admin-password" && command != "vpn-user" {
		return fmt.Errorf("usage: proxyk-admin [serve|init|healthcheck|reset-admin-password|vpn-user]")
	}
	store, err := sqlite.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.DB.Close()
	bootstrap := usecase.Bootstrap{Store: store}
	switch command {
	case "init":
		if cfg.PublicIP == "" {
			return fmt.Errorf("PUBLIC_IP is required for initialization")
		}
		path := filepath.Join(cfg.XrayDir, "config.json")
		if _, err := os.Stat(path); err == nil && store.Check(ctx) != nil {
			return fmt.Errorf("Xray configuration exists without an initialized database; restore the database before reinstalling")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		settings, err := bootstrap.Initialize(ctx, cfg.PublicIP, cfg.VPNPort, cfg.CredentialsPath())
		if err != nil {
			return err
		}
		// An existing Xray configuration belongs to the installed application.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return xray.WriteConfig(path, settings)
		} else {
			return err
		}
	case "reset-admin-password":
		resetCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		if err := bootstrap.ResetPassword(resetCtx, cfg.CredentialsPath()); err != nil {
			return err
		}
		slog.Info("administrator password changed; credentials saved to protected file")
		return nil
	case "vpn-user":
		if err := store.Check(ctx); err != nil {
			return fmt.Errorf("run init first: %w", err)
		}
		return runVPNUser(ctx, store, cfg.XrayAddr, os.Args[2:], os.Stdout)
	default:
		if err := store.Check(ctx); err != nil {
			return fmt.Errorf("run init first: %w", err)
		}
		return serve(ctx, cfg, store)
	}
}
