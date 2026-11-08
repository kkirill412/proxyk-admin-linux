package integration_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"proxyk-admin/internal/domain"
)

const stageTwoPayload = "response through VLESS"

func TestStageTwoVPNUserLifecycle(t *testing.T) {
	xray, app := os.Getenv("XRAY_BINARY"), os.Getenv("PROXYK_BINARY")
	if xray == "" || app == "" {
		t.Skip("run scripts/test-native.sh with the real Xray and ProxyK binaries")
	}
	// Both the REALITY target and the requested site stay on loopback.
	target := httptest.NewUnstartedServer(http.NotFoundHandler())
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	target.EnableHTTP2 = true
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}}
	target.StartTLS()
	t.Cleanup(target.Close)
	var requests atomic.Int64
	streamEnd := make(chan struct{})
	streamPayload := strings.Repeat("x", 256<<10)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/stream" {
			io.WriteString(w, streamPayload)
			w.(http.Flusher).Flush()
			select {
			case <-streamEnd:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, stageTwoPayload)
	}))
	t.Cleanup(site.Close)

	dir := t.TempDir()
	apiPort, appPort, vpnPort, proxyPort := stageTwoPorts(t)
	apiAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort))
	tlsDir := filepath.Join(dir, "tls")
	env := append(os.Environ(), "DATA_DIR="+filepath.Join(dir, "data"), "TLS_DIR="+tlsDir,
		"XRAY_CONFIG_DIR="+filepath.Join(dir, "xray"), "PUBLIC_IP=8.8.8.8",
		"VPN_PORT="+strconv.Itoa(vpnPort), "XRAY_ADDR="+apiAddr,
		fmt.Sprintf("LISTEN_ADDR=127.0.0.1:%d", appPort))
	run(t, env, app, "init")
	writeStageTwoCertificate(t, tlsDir)
	configPath := filepath.Join(dir, "xray", "config.json")
	configureLoopbackXray(t, configPath, apiAddr, vpnPort, target.Listener.Addr().String(), site.Listener.Addr().String())
	run(t, env, xray, "run", "-test", "-config", configPath)
	stopXray := start(t, env, xray, "run", "-config", configPath)
	connection, err := grpc.NewClient(apiAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	handlers := handler.NewHandlerServiceClient(connection)
	stopApp := start(t, env, app, "serve")

	name := "Тест & /?"
	id := strings.TrimSpace(string(run(t, env, app, "vpn-user", "create", name)))
	userID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || userID <= 0 {
		t.Fatal("create did not return a positive user ID")
	}
	link := stageTwoLink(t, env, app, id, name, vpnPort)
	email := "proxyk-user-" + id
	waitStageTwoUser(t, handlers, email, true)
	qrPath := filepath.Join(dir, "connection.png")
	run(t, env, app, "vpn-user", "qr", id, qrPath)
	if info, err := os.Stat(qrPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("QR was not saved privately")
	}
	clientConfig := writeStageTwoClient(t, dir, proxyPort, vpnPort, link)
	start(t, env, xray, "run", "-config", clientConfig)
	transport := &http.Transport{
		Proxy:             http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}),
		DisableKeepAlives: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	waitStageTwoPayload(t, client, site.URL)
	requireStageTwoTraffic(t, env, app, id)
	requireStageTwoAPIBlocked(t, proxyPort, apiAddr)

	renamed := "Новое имя"
	run(t, env, app, "vpn-user", "rename", id, renamed)
	renamedLink := stageTwoLink(t, env, app, id, renamed, vpnPort)
	if renamedLink.User.String() != link.User.String() || renamedLink.RawQuery != link.RawQuery {
		t.Fatal("rename changed the connection credentials")
	}
	listed := run(t, env, app, "vpn-user", "list")
	var users []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(listed, &users); err != nil || len(users) != 1 || users[0].ID != userID ||
		users[0].Name != renamed || !users[0].Enabled || strings.Contains(string(listed), link.User.Username()) {
		t.Fatal("list did not return the renamed user without connection credentials")
	}
	idleID := strings.TrimSpace(string(run(t, env, app, "vpn-user", "create", "Idle")))

	run(t, env, app, "vpn-user", "disable", id)
	waitStageTwoUser(t, handlers, email, false)
	requireStageTwoBlocked(t, client, site.URL, &requests)
	requireStageTwoNoLink(t, env, app, id)
	run(t, env, app, "vpn-user", "enable", id)
	waitStageTwoUser(t, handlers, email, true)
	waitStageTwoPayload(t, client, site.URL)

	beforeRestart := readStageTwoTraffic(t, env, app, id)
	stopXray()
	stopXray = start(t, env, xray, "run", "-config", configPath)
	waitStageTwoUser(t, handlers, email, true)
	waitStageTwoPayload(t, client, site.URL)
	waitStageTwoTraffic(t, env, app, id, beforeRestart.UplinkBytes+1, beforeRestart.DownlinkBytes+1)

	beforeQuota := readStageTwoTraffic(t, env, app, id)
	limit := beforeQuota.UplinkBytes + beforeQuota.DownlinkBytes + (64 << 10)
	run(t, env, app, "vpn-user", "limit", id, strconv.FormatInt(limit, 10))
	// Keep this connection open while its traffic exhausts the quota. Xray's
	// RemoveUser revokes new connections but does not terminate this stream.
	streamClient := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	stream, err := streamClient.Get(site.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if _, err := io.ReadFull(stream.Body, make([]byte, len(streamPayload))); err != nil {
		t.Fatal(err)
	}
	waitStageTwoUser(t, handlers, email, false)
	requireStageTwoBlocked(t, client, site.URL, &requests)
	requireStageTwoNoLink(t, env, app, id)
	close(streamEnd)
	tail, err := io.ReadAll(stream.Body)
	if err != nil || string(tail) != stageTwoPayload {
		t.Fatalf("existing tunnel was interrupted: %v", err)
	}
	blockedTraffic := waitStageTwoTraffic(t, env, app, id, beforeQuota.UplinkBytes+1, beforeQuota.DownlinkBytes+int64(len(streamPayload)))
	if blockedTraffic.UplinkBytes+blockedTraffic.DownlinkBytes < limit {
		t.Fatal("quota block before quota was consumed")
	}
	requireStageTwoCommandFailure(t, env, app, "enable", id)
	if idle := readStageTwoTraffic(t, env, app, idleID); idle.UplinkBytes != 0 || idle.DownlinkBytes != 0 {
		t.Fatal("traffic charged to another user")
	}

	stopApp()
	stopXray()
	stopXray = start(t, env, xray, "run", "-config", configPath)
	stopApp = start(t, env, app, "serve")
	waitStageTwoUser(t, handlers, "proxyk-user-"+idleID, true)
	waitStageTwoUser(t, handlers, email, false)
	waitStageTwoTraffic(t, env, app, id, blockedTraffic.UplinkBytes, blockedTraffic.DownlinkBytes)
	requireStageTwoNoLink(t, env, app, id)
	run(t, env, app, "vpn-user", "limit", id, "unlimited")
	requireStageTwoNoLink(t, env, app, id)
	run(t, env, app, "vpn-user", "enable", id)
	waitStageTwoPayload(t, client, site.URL)

	stopApp()
	stopXray()
	readStageTwoTraffic(t, env, app, id) // Saved totals remain readable with Xray stopped.
	start(t, env, xray, "run", "-config", configPath)
	waitStageTwoUser(t, handlers, email, false)
	requireStageTwoNoLink(t, env, app, id) // Old acknowledgement is insufficient after restart.
	requireStageTwoCommandFailure(t, env, app, "create", "Pending")
	var pendingUsers []domain.VPNUser
	if err := json.Unmarshal(run(t, env, app, "vpn-user", "list"), &pendingUsers); err != nil {
		t.Fatal(err)
	}
	var pendingID string
	for _, user := range pendingUsers {
		if user.Name == "Pending" && user.Pending {
			pendingID = strconv.FormatInt(user.ID, 10)
		}
	}
	if pendingID == "" {
		t.Fatal("failed create did not preserve a recoverable pending user")
	}
	requireStageTwoNoLink(t, env, app, pendingID)
	requireStageTwoCommandFailure(t, env, app, "delete", id)
	start(t, env, app, "serve")
	waitStageTwoUser(t, handlers, "proxyk-user-"+pendingID, true)
	stageTwoLink(t, env, app, pendingID, "Pending", vpnPort)
	waitStageTwoUser(t, handlers, email, false)
	requireStageTwoBlocked(t, client, site.URL, &requests)
	requireStageTwoNoLink(t, env, app, id)
}

func stageTwoPorts(t *testing.T) (api, app, vpn, proxy int) {
	t.Helper()
	ports := []*int{&api, &app, &vpn, &proxy}
	for _, port := range ports {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		*port = listener.Addr().(*net.TCPAddr).Port
	}
	return
}

func writeStageTwoCertificate(t *testing.T, dir string) {
	t.Helper()
	first := filepath.Join(dir, "first")
	if err := os.MkdirAll(first, 0700); err != nil {
		t.Fatal(err)
	}
	run(t, os.Environ(), "openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
		"-nodes", "-days", "1", "-subj", "/CN=8.8.8.8", "-addext", "subjectAltName=IP:8.8.8.8",
		"-keyout", filepath.Join(first, "privkey.pem"), "-out", filepath.Join(first, "fullchain.pem"))
	if err := os.Symlink("first", filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
}

func configureLoopbackXray(t *testing.T, path, apiAddr string, vpnPort int, targetAddr, siteAddr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["api"].(map[string]any)["listen"] = apiAddr
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	inbound["listen"], inbound["port"] = "127.0.0.1", vpnPort
	inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["target"] = targetAddr
	// Xray's normal private-address policy stays in place except for the test site.
	_, sitePort, err := net.SplitHostPort(siteAddr)
	if err != nil {
		t.Fatal(err)
	}
	config["outbounds"].([]any)[0].(map[string]any)["settings"] = map[string]any{
		"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}, "port": sitePort}},
	}
	writeStageTwoJSON(t, path, config)
}

func stageTwoLink(t *testing.T, env []string, app, id, name string, port int) *url.URL {
	t.Helper()
	link, err := url.Parse(strings.TrimSpace(string(run(t, env, app, "vpn-user", "link", id))))
	if err != nil || link.Scheme != "vless" || link.User == nil || link.User.Username() == "" ||
		link.Host != net.JoinHostPort("8.8.8.8", strconv.Itoa(port)) || link.Fragment != name {
		t.Fatal("link has invalid VLESS authority or label")
	}
	params := link.Query()
	if params.Get("security") != "reality" || params.Get("flow") != "xtls-rprx-vision" ||
		(params.Get("type") != "tcp" && params.Get("type") != "raw") || params.Get("encryption") != "none" ||
		params.Get("pbk") == "" || params.Get("sid") == "" || params.Get("fp") == "" || params.Get("sni") == "" {
		t.Fatal("link is missing VLESS Vision or REALITY parameters")
	}
	return link
}

func writeStageTwoClient(t *testing.T, dir string, proxyPort, vpnPort int, link *url.URL) string {
	t.Helper()
	params := link.Query()
	path := filepath.Join(dir, "xray-client.json")
	writeStageTwoJSON(t, path, map[string]any{
		"log":      map[string]any{"loglevel": "warning", "access": "none"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": proxyPort, "protocol": "http"}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{
				"address": "127.0.0.1", "port": vpnPort, "id": link.User.Username(),
				"flow": params.Get("flow"), "encryption": params.Get("encryption"),
			},
			"streamSettings": map[string]any{
				"network": params.Get("type"), "security": params.Get("security"),
				"realitySettings": map[string]any{
					"fingerprint": params.Get("fp"), "serverName": params.Get("sni"),
					"password": params.Get("pbk"), "shortId": params.Get("sid"), "spiderX": params.Get("spx"),
				},
			},
		}},
	})
	return path
}

func writeStageTwoJSON(t *testing.T, path string, config map[string]any) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func waitStageTwoUser(t *testing.T, handlers handler.HandlerServiceClient, email string, want bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		response, err := handlers.GetInboundUsers(ctx, &handler.GetInboundUserRequest{Tag: "vless-reality"})
		cancel()
		if err == nil {
			found := false
			for _, user := range response.GetUsers() {
				found = found || user.GetEmail() == email
			}
			if found == want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Xray did not synchronize user presence to %t", want)
}

func waitStageTwoPayload(t *testing.T, client *http.Client, address string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = stageTwoRequest(client, address)
		if lastErr == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("VLESS/REALITY did not deliver the HTTP payload: %v", lastErr)
}

func stageTwoRequest(client *http.Client, address string) error {
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(body) != stageTwoPayload {
		return fmt.Errorf("unexpected proxy response status %d", response.StatusCode)
	}
	return nil
}

func requireStageTwoBlocked(t *testing.T, client *http.Client, address string, requests *atomic.Int64) {
	t.Helper()
	before := requests.Load()
	if err := stageTwoRequest(client, address); err == nil || requests.Load() != before {
		t.Fatal("revoked VPN user reached the site through a new connection")
	}
}

func requireStageTwoTraffic(t *testing.T, env []string, app, id string) {
	t.Helper()
	traffic := waitStageTwoTraffic(t, env, app, id, 1, 1)
	waitStageTwoTraffic(t, env, app, id, traffic.UplinkBytes, traffic.DownlinkBytes)
}

func readStageTwoTraffic(t *testing.T, env []string, app, id string) domain.VPNTraffic {
	t.Helper()
	var traffic domain.VPNTraffic
	if err := json.Unmarshal(run(t, env, app, "vpn-user", "traffic", id), &traffic); err != nil {
		t.Fatal(err)
	}
	return traffic
}

func waitStageTwoTraffic(t *testing.T, env []string, app, id string, up, down int64) domain.VPNTraffic {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		traffic := readStageTwoTraffic(t, env, app, id)
		if traffic.UplinkBytes >= up && traffic.DownlinkBytes >= down {
			return traffic
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("persistent traffic did not reach expected totals")
	return domain.VPNTraffic{}
}

func requireStageTwoCommandFailure(t *testing.T, env []string, app string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, app, append([]string{"vpn-user"}, args...)...)
	command.Env = env
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || strings.Contains(string(output), "vless://") {
		t.Fatalf("expected a bounded failure from %s", args[0])
	}
}

func requireStageTwoAPIBlocked(t *testing.T, proxyPort int, apiAddr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", apiAddr, apiAddr); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("test client did not open an HTTP CONNECT tunnel: %v", err)
	}
	// A reachable gRPC listener responds with an HTTP/2 settings frame.
	if _, err := io.WriteString(conn, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); err == nil {
		if _, err := reader.ReadByte(); err == nil {
			t.Fatal("VPN client reached the private Xray management listener")
		}
	}
}

func requireStageTwoNoLink(t *testing.T, env []string, app, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, app, "vpn-user", "link", id)
	command.Env = env
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || strings.Contains(string(output), "vless://") {
		t.Fatal("link did not reject a disabled or deleted VPN user")
	}
}
