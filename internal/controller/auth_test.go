package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
)

func TestAdminSessionLifecycle(t *testing.T) {
	store := authTestStore(t)
	handler := authTestHandler(store)
	const loginBody = `{"username":"test-admin","password":"test-password"}`
	const loginPath = "/api/auth/login"
	const sessionPath = "/api/auth/session"
	const logoutPath = "/api/auth/logout"
	authRequest(t, handler, http.MethodGet, sessionPath, "", nil, "", "", http.StatusUnauthorized)
	authRequest(t, handler, http.MethodPost, loginPath, loginBody, nil, "", "https://other.example", http.StatusForbidden)
	authRequest(t, handler, http.MethodPost, loginPath, `{"username":"test-admin","password":"incorrect"}`,
		nil, "", "", http.StatusUnauthorized)
	loggedIn := authRequest(t, handler, http.MethodPost, loginPath, loginBody, nil, "", "https://panel.example", http.StatusOK)
	cookie, session := readLogin(t, loggedIn)
	// Session state comes from SQLite and survives a new controller instance.
	handler = authTestHandler(store)
	resumed := authRequest(t, handler, http.MethodGet, sessionPath, "", cookie, "", "", http.StatusOK)
	var actual domain.AdminSession
	if err := json.Unmarshal(resumed.Body.Bytes(), &actual); err != nil || actual != session {
		t.Fatalf("session was not restored: %v", err)
	}
	authRequest(t, handler, http.MethodPost, "/api/change", "", cookie, "", "", http.StatusForbidden)
	authRequest(t, handler, http.MethodPost, "/api/change", "", cookie, session.CSRFToken, "https://other.example", http.StatusForbidden)
	authRequest(t, handler, http.MethodPost, "/api/change", "", cookie, session.CSRFToken, "https://panel.example", http.StatusNoContent)
	authRequest(t, handler, http.MethodPost, logoutPath, "", cookie, "incorrect", "", http.StatusForbidden)
	logout := authRequest(t, handler, http.MethodPost, logoutPath, "", cookie, session.CSRFToken, "", http.StatusNoContent)
	if cookies := logout.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != cookie.Name || cookies[0].Value != "" || cookies[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the browser cookie")
	}
	authRequest(t, handler, http.MethodGet, sessionPath, "", cookie, "", "", http.StatusUnauthorized)

	second, _ := readLogin(t, authRequest(t, handler, http.MethodPost, loginPath, loginBody, nil, "", "", http.StatusOK))
	if _, err := store.DB.Exec(`UPDATE administrators SET credential_version = credential_version + 1`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, handler, http.MethodGet, sessionPath, "", second, "", "", http.StatusUnauthorized)

	third, _ := readLogin(t, authRequest(t, handler, http.MethodPost, loginPath, loginBody, nil, "", "", http.StatusOK))
	if _, err := store.DB.Exec(`UPDATE admin_sessions SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, handler, http.MethodGet, sessionPath, "", third, "", "", http.StatusUnauthorized)
}

func TestLoginRateLimitIgnoresForwardedAddress(t *testing.T) {
	handler := authTestHandler(authTestStore(t))
	for i := 0; i <= loginPerIP; i++ {
		request := httptest.NewRequest(http.MethodPost, "https://panel.example/api/auth/login",
			strings.NewReader(`{"username":"test-admin","password":"incorrect"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if i < loginPerIP && recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d returned %d", i+1, recorder.Code)
		}
		if i == loginPerIP && (recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "") {
			t.Fatalf("rate limit bypassed: status %d", recorder.Code)
		}
	}
}

func TestLoginRateLimitBoundsDistinctAddressesAndBcrypt(t *testing.T) {
	var limiter loginLimiter
	now := time.Now()
	for i := range loginParallel {
		if retry := limiter.begin(fmt.Sprintf("192.0.2.%d:443", i+1), now); retry != 0 {
			t.Fatal("valid bcrypt reservation rejected")
		}
	}
	if limiter.begin("192.0.2.200:443", now) <= 0 {
		t.Fatal("concurrent bcrypt cap bypassed")
	}
	for range loginParallel {
		limiter.end()
	}
	for i := loginParallel; i < loginTotal; i++ {
		if retry := limiter.begin(fmt.Sprintf("192.0.2.%d:443", i+1), now); retry != 0 {
			t.Fatal("available global attempt rejected")
		}
		limiter.end()
	}
	if limiter.begin("192.0.2.200:443", now) <= 0 {
		t.Fatal("global cap bypassed with a distinct address")
	}
	if limiter.begin("192.0.2.200:443", now.Add(loginWindow)) != 0 {
		t.Fatal("global cap did not reset after window expired")
	}
	limiter.end()
}

func authTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO administrators (id, username, password_hash) VALUES (1, ?, ?)`, "test-admin", string(hash)); err != nil {
		t.Fatal(err)
	}
	return store
}

func authTestHandler(store *sqlite.Store) http.Handler {
	auth := NewAuth(store)
	mux := http.NewServeMux()
	auth.Register(mux)
	mux.Handle("POST /api/change", auth.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	return mux
}

func authRequest(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie, csrf, origin string, status int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != status {
		t.Fatalf("%s %s returned %d, expected %d", method, path, recorder.Code, status)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s was not protected from caching", method, path)
	}
	return recorder
}

func readLogin(t *testing.T, recorder *httptest.ResponseRecorder) (*http.Cookie, domain.AdminSession) {
	t.Helper()
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("login did not set a session cookie")
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Path != "/" || cookie.Domain != "" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != int(sessionLifetime/time.Second) {
		t.Fatal("session cookie attributes are unsafe")
	}
	var session domain.AdminSession
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil || session.Username != "test-admin" || session.CSRFToken == "" || strings.Contains(recorder.Body.String(), cookie.Value) {
		t.Fatalf("invalid session response: %v", err)
	}
	return cookie, session
}
