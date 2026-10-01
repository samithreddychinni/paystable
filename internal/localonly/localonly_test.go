package localonly

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrap(t *testing.T) {
	h := Wrap([]net.IP{net.ParseIP("172.31.0.1")}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for addr, want := range map[string]int{
		"127.0.0.1:5050":   http.StatusOK,
		"[::1]:5050":       http.StatusOK,
		"172.31.0.1:5050":  http.StatusOK,
		"172.31.0.2:5050":  http.StatusForbidden,
		"203.0.113.5:5050": http.StatusForbidden,
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("addr %s: got %d, want %d", addr, w.Code, want)
		}
	}
}
