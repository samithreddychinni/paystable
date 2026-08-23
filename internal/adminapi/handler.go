package adminapi

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"

	"github.com/IDEA-Amrita/paystable/internal/config"
)

// Handler serves the read-only dashboard API plus the two write actions
// (replay delivery, rotate secret). Everything here is gated to loopback.
type Handler struct {
	db  *sql.DB
	cfg *config.Config
}

func New(db *sql.DB, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// Register wires all /api/v1/admin routes onto mux, each behind the localhost gate.
func (h *Handler) Register(mux *http.ServeMux) {
	g := localhostOnly

	mux.Handle("GET /api/v1/admin/overview/stats", g(http.HandlerFunc(h.overviewStats)))
	mux.Handle("GET /api/v1/admin/transactions", g(http.HandlerFunc(h.transactions)))
	mux.Handle("GET /api/v1/admin/transactions/{id}", g(http.HandlerFunc(h.transactionDetail)))
	mux.Handle("GET /api/v1/admin/mismatches", g(http.HandlerFunc(h.mismatches)))
	mux.Handle("GET /api/v1/admin/mismatches/stats", g(http.HandlerFunc(h.mismatchStats)))
	mux.Handle("GET /api/v1/admin/deliveries", g(http.HandlerFunc(h.deliveries)))
	mux.Handle("GET /api/v1/admin/deliveries/stats", g(http.HandlerFunc(h.deliveryStats)))
	mux.Handle("POST /api/v1/admin/deliveries/{id}/replay", g(http.HandlerFunc(h.replayDelivery)))
	mux.Handle("GET /api/v1/admin/config", g(http.HandlerFunc(h.config)))
	mux.Handle("POST /api/v1/admin/config", g(http.HandlerFunc(h.updateConfig)))
	mux.Handle("GET /api/v1/admin/config/rotation-status", g(http.HandlerFunc(h.rotationStatus)))
	mux.Handle("POST /api/v1/admin/config/rotate-secret", g(http.HandlerFunc(h.rotateSecret)))
	mux.Handle("POST /api/v1/admin/verification/demo", g(http.HandlerFunc(h.verificationDemo)))
	mux.Handle("GET /api/v1/admin/export/ledger", g(http.HandlerFunc(h.ExportLedger)))
	mux.HandleFunc("GET /api/v1/transactions/{id}/timeline", h.PublicTimeline)
}

// localhostOnly rejects any request whose remote address is not loopback.
func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, `{"error":"dashboard is available on localhost only"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
