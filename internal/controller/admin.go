package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"proxyk-admin/internal/domain"
	"proxyk-admin/internal/service"
	"proxyk-admin/internal/usecase"
)

type AdminAPI struct {
	Auth  *Auth
	Users usecase.VPNUsers
}

func (a AdminAPI) register(mux *http.ServeMux) {
	a.Auth.Register(mux)
	mux.Handle("GET /api/users", a.Auth.Require(http.HandlerFunc(a.listUsers)))
	mux.Handle("POST /api/users", a.Auth.Require(http.HandlerFunc(a.createUser)))
	mux.Handle("PATCH /api/users/{id}", a.Auth.Require(http.HandlerFunc(a.updateUser)))
	mux.Handle("DELETE /api/users/{id}", a.Auth.Require(http.HandlerFunc(a.deleteUser)))
	mux.Handle("GET /api/users/{id}/connection", a.Auth.Require(http.HandlerFunc(a.userConnection)))
}

func (a AdminAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.Users.Store.ListVPNUsers(r.Context())
	if err != nil {
		writeUserError(w, 0, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (a AdminAPI) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Limit *int64 `json:"traffic_limit_bytes"`
	}
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Проверьте имя и лимит трафика.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	user, err := a.Users.Create(ctx, input.Name, input.Limit)
	if err != nil {
		if user.ID > 0 {
			writeSavedUser(w, user.ID)
		} else {
			writeUserError(w, 0, err)
		}
		return
	}
	result := struct {
		domain.VPNUser
		Connection *connectionDetails `json:"connection,omitempty"`
	}{VPNUser: user}
	if user.Allowed() {
		result.Connection, err = a.connection(ctx, user.ID)
		if err != nil {
			writeSavedUser(w, user.ID)
			return
		}
	}
	writeJSON(w, http.StatusCreated, result)
}

func (a AdminAPI) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if err := readJSON(w, r, &fields); err != nil || len(fields) != 1 {
		writeError(w, http.StatusBadRequest, "Выберите одно поле для изменения.")
		return
	}
	if err := a.applyUserUpdate(r.Context(), id, fields); err != nil {
		writeUserError(w, id, err)
		return
	}
	user, err := a.Users.Store.VPNUser(r.Context(), id)
	if err != nil {
		writeUserError(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

var errUserUpdate = errors.New("invalid user update")

func (a AdminAPI) applyUserUpdate(ctx context.Context, id int64, fields map[string]json.RawMessage) error {
	if raw, ok := fields["name"]; ok {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return domain.ErrVPNUserName
		}
		return a.Users.Rename(ctx, id, name)
	}
	if raw, ok := fields["traffic_limit_bytes"]; ok {
		var limit *int64
		if err := json.Unmarshal(raw, &limit); err != nil {
			return domain.ErrInvalidTrafficLimit
		}
		return a.Users.SetLimit(ctx, id, limit)
	}
	if raw, ok := fields["enabled"]; ok {
		var enabled *bool
		if err := json.Unmarshal(raw, &enabled); err != nil || enabled == nil {
			return errUserUpdate
		}
		return a.Users.SetEnabled(ctx, id, *enabled)
	}
	return errUserUpdate
}

func (a AdminAPI) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	if err := a.Users.Delete(r.Context(), id); err != nil {
		writeUserError(w, id, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

type connectionDetails struct {
	Link      string `json:"link"`
	QRDataURL string `json:"qr_data_url"`
}

func (a AdminAPI) connection(ctx context.Context, id int64) (*connectionDetails, error) {
	link, err := a.Users.Link(ctx, id)
	if err != nil {
		return nil, err
	}
	qr, err := (service.Connections{}).QR(link)
	if err != nil {
		return nil, err
	}
	return &connectionDetails{Link: link, QRDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(qr)}, nil
}

func (a AdminAPI) userConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	connection, err := a.connection(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrVPNSyncPending) {
			writeError(w, http.StatusServiceUnavailable, "Дождитесь подтверждения Xray, чтобы получить подключение.")
			return
		}
		writeUserError(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, connection)
}

func userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Неверный номер пользователя.")
		return 0, false
	}
	return id, true
}

func writeSavedUser(w http.ResponseWriter, id int64) {
	writeJSON(w, http.StatusServiceUnavailable, struct {
		Error string `json:"error"`
		ID    int64  `json:"id"`
	}{"Изменение сохранено. Дождитесь подтверждения Xray и обновите список.", id})
}

func writeUserError(w http.ResponseWriter, id int64, err error) {
	switch {
	case errors.Is(err, domain.ErrVPNUserNotFound):
		writeError(w, http.StatusNotFound, "Пользователь не найден.")
	case errors.Is(err, domain.ErrVPNUserName):
		writeError(w, http.StatusBadRequest, "Введите имя от 1 до 80 символов без управляющих знаков.")
	case errors.Is(err, domain.ErrInvalidTrafficLimit):
		writeError(w, http.StatusBadRequest, "Лимит должен быть неотрицательным целым числом байтов.")
	case errors.Is(err, errUserUpdate):
		writeError(w, http.StatusBadRequest, "Некорректное изменение пользователя.")
	case errors.Is(err, domain.ErrTrafficLimit):
		writeError(w, http.StatusConflict, "Лимит исчерпан. Увеличьте или снимите лимит перед разблокировкой.")
	case errors.Is(err, domain.ErrVPNUserBlocked):
		writeError(w, http.StatusConflict, "Подключение недоступно для заблокированного пользователя.")
	case errors.Is(err, domain.ErrVPNSyncPending):
		writeSavedUser(w, id)
	default:
		writeError(w, http.StatusServiceUnavailable, "Операция недоступна. Проверьте состояние панели и VPN.")
	}
}
