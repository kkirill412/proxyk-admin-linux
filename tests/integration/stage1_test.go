package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func run(t *testing.T, env []string, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", filepath.Base(name), err, output)
	}
	return output
}

func start(t *testing.T, env []string, name string, args ...string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	log, err := os.CreateTemp(t.TempDir(), "process.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("%s: %s", filepath.Base(name), data)
		}
	}
	t.Cleanup(stop)
	return stop
}

func waitHTTP(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d", url, want)
}

func TestStageOneWithRealXray(t *testing.T) {
	xray, app := os.Getenv("XRAY_BINARY"), os.Getenv("PROXYK_BINARY")
	if xray == "" || app == "" {
		t.Skip("run scripts/test-native.sh with the real Xray binary")
	}
	t.Log(strings.TrimSpace(string(run(t, os.Environ(), xray, "version"))))
	dir := t.TempDir()
	dataDir, xrayDir, tlsDir := filepath.Join(dir, "data"), filepath.Join(dir, "xray"), filepath.Join(dir, "tls")
	apiPort, appPort, vpnPort := freePort(t), freePort(t), freePort(t)
	env := append(os.Environ(), "DATA_DIR="+dataDir, "XRAY_CONFIG_DIR="+xrayDir, "TLS_DIR="+tlsDir,
		"PUBLIC_IP=8.8.8.8", "VPN_PORT=443", fmt.Sprintf("XRAY_ADDR=127.0.0.1:%d", apiPort), fmt.Sprintf("LISTEN_ADDR=127.0.0.1:%d", appPort))
	output := run(t, env, app, "init")
	credentials, err := os.ReadFile(filepath.Join(dataDir, "initial-credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var creds map[string]string
	if err := json.Unmarshal(credentials, &creds); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), creds["password"]) {
		t.Fatal("init leaked password")
	}
	run(t, env, app, "init")
	after, err := os.ReadFile(filepath.Join(dataDir, "initial-credentials.json"))
	if err != nil || string(after) != string(credentials) {
		t.Fatal("reinstall changed credentials")
	}
	configPath := filepath.Join(xrayDir, "config.json")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatal(err)
	}
	config["api"].(map[string]any)["listen"] = fmt.Sprintf("127.0.0.1:%d", apiPort)
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	inbound["port"], inbound["listen"] = vpnPort, "127.0.0.1"
	configData, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	run(t, os.Environ(), xray, "run", "-test", "-config", configPath)
	stopXray := start(t, os.Environ(), xray, "run", "-config", configPath)
	if err := os.MkdirAll(filepath.Join(tlsDir, "first"), 0700); err != nil {
		t.Fatal(err)
	}
	run(t, os.Environ(), "openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "1",
		"-subj", "/CN=8.8.8.8", "-addext", "subjectAltName=IP:8.8.8.8", "-keyout", filepath.Join(tlsDir, "first", "privkey.pem"), "-out", filepath.Join(tlsDir, "first", "fullchain.pem"))
	if err := os.Symlink("first", filepath.Join(tlsDir, "current")); err != nil {
		t.Fatal(err)
	}
	start(t, env, app, "serve")
	cert, err := os.ReadFile(filepath.Join(tlsDir, "first", "fullchain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "8.8.8.8", MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	url := fmt.Sprintf("https://127.0.0.1:%d", appPort)
	waitHTTP(t, client, url+"/health/ready", 200)
	run(t, env, app, "healthcheck")
	waitHTTP(t, client, url+"/", 200)
	response, err := client.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !strings.Contains(string(body), "/assets/") {
		t.Fatal("embedded Vue assets not served")
	}
	stopXray()
	waitHTTP(t, client, url+"/health/ready", 503)
	waitHTTP(t, client, url+"/health/live", 200)
	start(t, os.Environ(), xray, "run", "-config", configPath)
	waitHTTP(t, client, url+"/health/ready", 200)
	output = run(t, env, app, "reset-admin-password")
	if strings.Contains(string(output), creds["password"]) {
		t.Fatal("reset leaked password")
	}
	// Losing the database must not silently rotate keys beside an old Xray config.
	orphanDir := filepath.Join(dir, "orphan-data")
	command := exec.Command(app, "init")
	command.Env = append(env, "DATA_DIR="+orphanDir)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("initialization accepted an orphaned Xray configuration: %s", output)
	}
	if _, err := os.Stat(filepath.Join(orphanDir, "initial-credentials.json")); !os.IsNotExist(err) {
		t.Fatal("orphaned configuration generated a new administrator")
	}
}
