package integration_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"proxyk-admin/internal/domain"
)

type webFixture struct {
	env                                         []string
	xray, xrayConfig, dataDir, tlsDir, panelURL string
	stopXray                                    func()
	vpnPort                                     int
	siteURL                                     string
	siteRequests                                *atomic.Int64
}

type webClient struct {
	*http.Client
	baseURL string
	csrf    string
}

type webSession struct {
	Username string `json:"username"`
	CSRF     string `json:"csrf_token"`
}

type webConnection struct {
	Link string `json:"link"`
	QR   string `json:"qr_data_url"`
}

func checkWebAdministration(t *testing.T, fixture webFixture) {
	panel := newWebClient(t, fixture.panelURL, fixture.tlsDir)
	panel.request(t, http.MethodGet, "/api/users", nil, http.StatusUnauthorized, nil)
	panel.request(t, http.MethodGet, "/api/auth/session", nil, http.StatusUnauthorized, nil)
	cookie := loginToPanel(t, panel, fixture.dataDir)

	const name = "Ноутбук & телефон"
	payload := map[string]any{"name": name, "traffic_limit_bytes": nil}
	csrf := panel.csrf
	panel.csrf = ""
	panel.request(t, http.MethodPost, "/api/users", payload, http.StatusForbidden, nil)
	panel.csrf = csrf
	var created struct {
		domain.VPNUser
		Connection webConnection `json:"connection"`
	}
	panel.request(t, http.MethodPost, "/api/users", payload, http.StatusCreated, &created)
	if created.ID <= 0 || created.Name != name || !created.Enabled || created.Pending || created.TrafficLimitBytes != nil {
		t.Fatal("web creation did not confirm an active, unlimited user")
	}
	path := fmt.Sprintf("/api/users/%d", created.ID)
	link := checkWebConnection(t, created.Connection, name)
	var connection webConnection
	response := panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusOK, &connection)
	if response.Header.Get("Cache-Control") != "no-store" || connection.Link != created.Connection.Link {
		t.Fatal("connection response changed credentials or could be cached")
	}
	checkWebConnection(t, connection, name)

	client := startWebTunnel(t, fixture, link)
	waitStageTwoPayload(t, client, fixture.siteURL)
	waitWebTraffic(t, panel, created.ID, link.User.Username())

	var updated domain.VPNUser
	panel.request(t, http.MethodPatch, path, map[string]string{"name": "Новое имя"}, http.StatusOK, &updated)
	if updated.Name != "Новое имя" {
		t.Fatal("web rename did not update the user")
	}
	panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusOK, &connection)
	renamedLink := checkWebConnection(t, connection, updated.Name)
	if renamedLink.User.Username() != link.User.Username() {
		t.Fatal("web rename changed the VPN credential")
	}

	panel.request(t, http.MethodPatch, path, map[string]int64{"traffic_limit_bytes": 1 << 30}, http.StatusOK, &updated)
	if updated.TrafficLimitBytes == nil || *updated.TrafficLimitBytes != 1<<30 || !updated.Enabled {
		t.Fatal("web update did not save the quota")
	}
	panel.request(t, http.MethodPatch, path, map[string]bool{"enabled": false}, http.StatusOK, &updated)
	if updated.Enabled || updated.Pending || updated.BlockedReason != "manual" {
		t.Fatal("web block did not confirm removal from Xray")
	}
	panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusConflict, nil)
	requireStageTwoBlocked(t, client, fixture.siteURL, fixture.siteRequests)

	panel.request(t, http.MethodPatch, path, map[string]bool{"enabled": true}, http.StatusOK, &updated)
	waitStageTwoPayload(t, client, fixture.siteURL)
	panel.request(t, http.MethodPatch, path, map[string]int64{"traffic_limit_bytes": 0}, http.StatusOK, &updated)
	if updated.Enabled || updated.BlockedReason != "limit" {
		t.Fatal("exhausted quota did not block the web-managed user")
	}
	panel.request(t, http.MethodPatch, path, map[string]bool{"enabled": true}, http.StatusConflict, nil)
	panel.request(t, http.MethodDelete, path, nil, http.StatusNoContent, nil)
	panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusNotFound, nil)
	checkPendingWebCreation(t, panel, fixture)

	panel.request(t, http.MethodPost, "/api/auth/logout", nil, http.StatusNoContent, nil)
	panel.request(t, http.MethodGet, "/api/users", nil, http.StatusUnauthorized, nil)
	base, err := url.Parse(panel.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	panel.Jar.SetCookies(base, []*http.Cookie{cookie})
	panel.request(t, http.MethodGet, "/api/auth/session", nil, http.StatusUnauthorized, nil)
}

func checkPendingWebCreation(t *testing.T, panel *webClient, fixture webFixture) {
	t.Helper()
	fixture.stopXray()
	var saved struct {
		ID         int64          `json:"id"`
		Connection *webConnection `json:"connection"`
	}
	const name = "Ожидает Xray"
	panel.request(t, http.MethodPost, "/api/users", map[string]string{"name": name}, http.StatusServiceUnavailable, &saved)
	if saved.ID <= 0 || saved.Connection != nil {
		t.Fatal("pending creation did not return the saved ID without credentials")
	}
	path := fmt.Sprintf("/api/users/%d", saved.ID)
	panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusServiceUnavailable, nil)
	start(t, fixture.env, fixture.xray, "run", "-config", fixture.xrayConfig)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var users []domain.VPNUser
		panel.request(t, http.MethodGet, "/api/users", nil, http.StatusOK, &users)
		for _, user := range users {
			if user.ID == saved.ID && !user.Pending {
				var connection webConnection
				panel.request(t, http.MethodGet, path+"/connection", nil, http.StatusOK, &connection)
				checkWebConnection(t, connection, name)
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("pending web user did not recover after Xray restart")
}

func newWebClient(t *testing.T, baseURL, tlsDir string) *webClient {
	t.Helper()
	cert, err := os.ReadFile(filepath.Join(tlsDir, "first", "fullchain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("could not trust the panel test certificate")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout: 15 * time.Second, Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "8.8.8.8", MinVersion: tls.VersionTLS12},
		},
	}
	t.Cleanup(client.CloseIdleConnections)
	return &webClient{Client: client, baseURL: baseURL}
}

func loginToPanel(t *testing.T, panel *webClient, dataDir string) *http.Cookie {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, "initial-credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var credentials domain.Credentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		t.Fatal("could not parse administrator credentials")
	}
	var session webSession
	response := panel.request(t, http.MethodPost, "/api/auth/login", credentials, http.StatusOK, &session)
	if session.Username != credentials.Username || session.CSRF == "" {
		t.Fatal("login did not return the administrator session")
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/" ||
		(cookies[0].SameSite != http.SameSiteStrictMode && cookies[0].SameSite != http.SameSiteLaxMode) {
		t.Fatal("login did not issue a protected session cookie")
	}
	panel.request(t, http.MethodGet, "/api/auth/session", nil, http.StatusOK, &session)
	if session.Username != credentials.Username || session.CSRF == "" {
		t.Fatal("session could not be restored from the cookie")
	}
	panel.csrf = session.CSRF
	return cookies[0]
}

func (p *webClient) request(t *testing.T, method, path string, payload any, want int, output any) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, p.baseURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", p.baseURL)
		request.Header.Set("X-CSRF-Token", p.csrf)
	}
	response, err := p.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("%s %s returned %d, want %d", method, path, response.StatusCode, want)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatalf("%s %s returned invalid JSON: %v", method, path, err)
		}
	}
	return response
}

func checkWebConnection(t *testing.T, connection webConnection, name string) *url.URL {
	t.Helper()
	link, err := url.Parse(connection.Link)
	if err != nil || link.Scheme != "vless" || link.User == nil || link.User.Username() == "" || link.Fragment != name {
		t.Fatal("panel returned an invalid VLESS link")
	}
	encoded, found := strings.CutPrefix(connection.QR, "data:image/png;base64,")
	if !found {
		t.Fatal("panel did not return an inline PNG QR code")
	}
	pngReader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	img, err := png.Decode(pngReader)
	if err != nil {
		t.Fatal("panel returned an invalid QR image")
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal("panel QR image could not be decoded")
	}
	// The input is a generated PNG, so decode its grid directly.
	qr, err := qrcode.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_PURE_BARCODE: true})
	if err != nil || qr.GetText() != connection.Link {
		t.Fatalf("panel QR code did not contain the VLESS link: %v", err)
	}
	return link
}

func startWebTunnel(t *testing.T, fixture webFixture, link *url.URL) *http.Client {
	t.Helper()
	proxyPort := freePort(t)
	config := writeStageTwoClient(t, t.TempDir(), proxyPort, fixture.vpnPort, link)
	start(t, fixture.env, fixture.xray, "run", "-config", config)
	transport := &http.Transport{
		Proxy:             http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}),
		DisableKeepAlives: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func waitWebTraffic(t *testing.T, panel *webClient, id int64, credential string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var data json.RawMessage
		panel.request(t, http.MethodGet, "/api/users", nil, http.StatusOK, &data)
		if bytes.Contains(data, []byte(credential)) {
			t.Fatal("user list exposed a VLESS credential")
		}
		var users []domain.VPNUser
		if err := json.Unmarshal(data, &users); err != nil {
			t.Fatal("invalid web user list")
		}
		for _, user := range users {
			if user.ID == id && user.UplinkBytes > 0 && user.DownlinkBytes > 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("web list did not show real tunnel traffic")
}
