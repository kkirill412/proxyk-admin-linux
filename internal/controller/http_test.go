package controller_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"proxyk-admin/internal/controller"
	"proxyk-admin/internal/usecase"
)

type checker struct{ err error }

func (c checker) Check(context.Context) error { return c.err }

func TestReadinessRequiresDatabaseAndXray(t *testing.T) {
	for _, test := range []struct {
		name     string
		db, xray error
		status   int
	}{
		{"healthy", nil, nil, 200},
		{"database unavailable", errors.New("private database detail"), nil, 503},
		{"xray unavailable", nil, errors.New("private API detail"), 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := controller.Handler(usecase.Health{Database: checker{test.db}, Xray: checker{test.xray}}, fstest.MapFS{"index.html": {Data: []byte("frontend")}})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/health/ready", nil))
			if recorder.Code != test.status {
				t.Fatalf("status %d, want %d", recorder.Code, test.status)
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness can be cached")
			}
			for _, path := range []string{"/api/users", "/health/unknown", "/initial-credentials.json"} {
				recorder = httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
				if recorder.Code != http.StatusNotFound {
					t.Fatalf("%s returned %d", path, recorder.Code)
				}
			}
		})
	}
}
