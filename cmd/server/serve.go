package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/adapter/xray"
	"proxyk-admin/internal/config"
	"proxyk-admin/internal/controller"
	"proxyk-admin/internal/service"
	"proxyk-admin/internal/usecase"
	"proxyk-admin/web"
)

func serve(ctx context.Context, cfg config.Config, store *sqlite.Store) error {
	if _, err := service.LoadCertificate(cfg.TLSDir, cfg.PublicIP); err != nil {
		return err
	}
	xrayClient, err := xray.New(cfg.XrayAddr)
	if err != nil {
		return err
	}
	defer xrayClient.Close()
	frontend, err := web.Files()
	if err != nil {
		return err
	}
	lock, err := service.CollectorLock(filepath.Join(cfg.DataDir, "traffic.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	collector := &usecase.TrafficCollector{Store: store, Xray: xrayClient,
		JournalPath: filepath.Join(cfg.DataDir, "traffic-pending.json")}
	userSync := &usecase.VPNUserSync{Store: store, Xray: xrayClient, Traffic: collector}
	syncCtx, cancelSync := context.WithCancel(ctx)
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		userSync.Run(syncCtx)
	}()
	defer func() {
		cancelSync()
		<-syncDone
	}()
	server := &http.Server{
		Addr: cfg.ListenAddr, Handler: controller.Handler(usecase.Health{Database: store, Xray: userSync}, frontend),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return service.LoadCertificate(cfg.TLSDir, cfg.PublicIP)
		}},
	}
	stopped := make(chan struct{})
	shutdownComplete := make(chan struct{})
	go func() {
		defer close(shutdownComplete)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				server.Close()
			}
		case <-stopped:
		}
	}()
	defer close(stopped)
	slog.Info("HTTPS server starting", "address", cfg.ListenAddr)
	err = server.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownComplete
		return nil
	}
	return err
}

func healthcheck(ctx context.Context, cfg config.Config) error {
	cert, err := os.ReadFile(filepath.Join(cfg.TLSDir, "current", "fullchain.pem"))
	if err != nil {
		return err
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(cert) {
		return fmt.Errorf("invalid healthcheck CA certificate")
	}
	_, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: cfg.PublicIP},
	}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://127.0.0.1:"+port+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned %d", resp.StatusCode)
	}
	return nil
}
