package adminapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IDEA-Amrita/paystable/internal/localonly"
)

func TestLocalhostOnly_AllowsLoopback(t *testing.T) {
	called := false
	h := localonly.Wrap(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"127.0.0.1:5050", "[::1]:5050"} {
		req := httptest.NewRequest("GET", "/api/v1/admin/config", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		called = false
		h.ServeHTTP(w, req)
		if !called || w.Code != http.StatusOK {
			t.Errorf("addr %s: expected pass, got code %d called=%v", addr, w.Code, called)
		}
	}
}

func TestLocalhostOnly_RejectsExternal(t *testing.T) {
	h := localonly.Wrap(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"203.0.113.5:5050", "10.0.0.4:5050", "192.168.1.10:443"} {
		req := httptest.NewRequest("GET", "/api/v1/admin/config", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("addr %s: expected 403, got %d", addr, w.Code)
		}
	}
}
