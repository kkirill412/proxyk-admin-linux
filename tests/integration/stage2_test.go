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
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
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

	name := "Тест & /?"
	id := strings.TrimSpace(string(run(t, env, app, "vpn-user", "create", name)))
	userID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || userID <= 0 {
		t.Fatal("create did not return a positive user ID")
	}
	link := stageTwoLink(t, env, app, id, name, vpnPort)
	email := "proxyk-user-" + id
	stopApp := start(t, env, app, "serve")
	waitStageTwoUser(t, handlers, email, true)
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

	run(t, env, app, "vpn-user", "disable", id)
	waitStageTwoUser(t, handlers, email, false)
	requireStageTwoBlocked(t, client, site.URL, &requests)
	requireStageTwoNoLink(t, env, app, id)
	run(t, env, app, "vpn-user", "enable", id)
	waitStageTwoUser(t, handlers, email, true)
	waitStageTwoPayload(t, client, site.URL)

	stopXray()
	start(t, env, xray, "run", "-config", configPath)
	waitStageTwoUser(t, handlers, email, true)
	waitStageTwoPayload(t, client, site.URL)

	stopApp()
	run(t, env, app, "vpn-user", "delete", id)
	start(t, env, app, "serve")
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
	var traffic struct {
		Uplink   int64 `json:"uplink_bytes"`
		Downlink int64 `json:"downlink_bytes"`
	}
	for range 2 {
		previous := traffic
		output := run(t, env, app, "vpn-user", "traffic", id)
		if err := json.Unmarshal(output, &traffic); err != nil || traffic.Uplink <= 0 || traffic.Downlink <= 0 ||
			traffic.Uplink < previous.Uplink || traffic.Downlink < previous.Downlink {
			t.Fatal("traffic must report both Xray counters without resetting them")
		}
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
