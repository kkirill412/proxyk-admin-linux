package controller

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
	"proxyk-admin/internal/adapter/repository/sqlite"
	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
)

const (
	sessionCookieName = "__Host-proxyk-session"
	sessionTokenBytes = 32
	sessionLifetime   = 12 * time.Hour
)

type Auth struct {
	store   *sqlite.Store
	origin  http.CrossOriginProtection
	limiter loginLimiter
}

type adminSessionKey struct{}

func NewAuth(store *sqlite.Store) *Auth {
	return &Auth{store: store}
}

func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.Handle("GET /api/auth/session", a.Require(http.HandlerFunc(a.session)))
	mux.Handle("POST /api/auth/logout", a.Require(http.HandlerFunc(a.logout)))
}

func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || len(cookie.Value) != base64.RawURLEncoding.EncodedLen(sessionTokenBytes) {
			writeError(w, http.StatusUnauthorized, "Войдите в панель")
			return
		}
		hash := sha256.Sum256([]byte(cookie.Value))
		session, err := a.store.AdminSession(r.Context(), hash[:], time.Now())
		if errors.Is(err, domain.ErrAdminSessionInvalid) {
			writeError(w, http.StatusUnauthorized, "Сессия завершена. Войдите снова")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Не удалось проверить сессию")
			return
		}
		if a.origin.Check(r) != nil || !validCSRF(r, session.CSRFToken) {
			writeError(w, http.StatusForbidden, "Проверка безопасности запроса не пройдена")
			return
		}
		ctx := context.WithValue(r.Context(), adminSessionKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validCSRF(r *http.Request, csrfToken string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrfToken)) == 1
	}
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.origin.Check(r) != nil {
		writeError(w, http.StatusForbidden, "Проверка безопасности запроса не пройдена")
		return
	}
	var input domain.Credentials
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Укажите логин и пароль")
		return
	}
	if input.Username == "" || len(input.Username) > 128 || input.Password == "" || len(input.Password) > 72 {
		writeError(w, http.StatusUnauthorized, "Неверный логин или пароль")
		return
	}
	if retry := a.limiter.begin(r.RemoteAddr, time.Now()); retry > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64((retry+time.Second-1)/time.Second), 10))
		writeError(w, http.StatusTooManyRequests, "Слишком много попыток. Попробуйте позже")
		return
	}
	defer a.limiter.end()
	admin, err := a.store.Administrator(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Не удалось проверить учётные данные")
		return
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(input.Password))
	if passwordErr != nil || input.Username != admin.Username {
		writeError(w, http.StatusUnauthorized, "Неверный логин или пароль")
		return
	}
	if err := a.startSession(w, r, admin); err != nil {
		if errors.Is(err, domain.ErrAdminSessionInvalid) {
			writeError(w, http.StatusUnauthorized, "Неверный логин или пароль")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "Не удалось создать сессию")
	}
}

func (a *Auth) startSession(w http.ResponseWriter, r *http.Request, admin domain.Administrator) error {
	token, err := service.RandomToken(sessionTokenBytes)
	if err != nil {
		return err
	}
	csrf, err := service.RandomToken(sessionTokenBytes)
	if err != nil {
		return err
	}
	now := time.Now()
	expires := now.Add(sessionLifetime)
	hash := sha256.Sum256([]byte(token))
	if err := a.store.CreateAdminSession(r.Context(), hash[:], csrf, admin.CredentialVersion, now, expires); err != nil {
		return err
	}
	http.SetCookie(w, newSessionCookie(token, expires, int(sessionLifetime/time.Second)))
	writeJSON(w, http.StatusOK, domain.AdminSession{Username: admin.Username, CSRFToken: csrf})
	return nil
}

func (a *Auth) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, r.Context().Value(adminSessionKey{}))
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookieName)
	hash := sha256.Sum256([]byte(cookie.Value))
	if err := a.store.DeleteAdminSession(r.Context(), hash[:]); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Не удалось завершить сессию")
		return
	}
	http.SetCookie(w, newSessionCookie("", time.Unix(1, 0), -1))
	w.WriteHeader(http.StatusNoContent)
}

func newSessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", Expires: expires,
		MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}
