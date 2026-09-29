package adminapi

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/localonly"
)

// Handler serves the read-only dashboard API plus the two write actions
// (replay delivery, rotate secret). Everything here is limited to local operators.
type Handler struct {
	db  *sql.DB
	cfg *config.Config
}

func New(db *sql.DB, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// Register wires all /api/v1/admin routes onto mux behind the operator access gate.
func (h *Handler) Register(mux *http.ServeMux) {
	g := func(next http.Handler) http.Handler { return localonly.Wrap(h.cfg.AdminAllowedIPs, next) }

	mux.Handle("GET /api/v1/admin/overview/stats", g(http.HandlerFunc(h.overviewStats)))
	mux.Handle("GET /api/v1/admin/transactions", g(http.HandlerFunc(h.transactions)))
	mux.Handle("GET /api/v1/admin/transactions/{id}", g(http.HandlerFunc(h.transactionDetail)))
	mux.Handle("GET /api/v1/admin/mismatches", g(http.HandlerFunc(h.mismatches)))
	mux.Handle("GET /api/v1/admin/mismatches/stats", g(http.HandlerFunc(h.mismatchStats)))
	mux.Handle("GET /api/v1/admin/reviews", g(http.HandlerFunc(h.reviews)))
	mux.Handle("POST /api/v1/admin/reviews/{id}", g(http.HandlerFunc(h.resolveReview)))
	mux.Handle("GET /api/v1/admin/deliveries", g(http.HandlerFunc(h.deliveries)))
	mux.Handle("GET /api/v1/admin/deliveries/stats", g(http.HandlerFunc(h.deliveryStats)))
	mux.Handle("POST /api/v1/admin/deliveries/{id}/replay", g(http.HandlerFunc(h.replayDelivery)))
	mux.Handle("GET /api/v1/admin/config", g(http.HandlerFunc(h.config)))
	mux.Handle("POST /api/v1/admin/config", g(http.HandlerFunc(h.configReadOnly)))
	mux.Handle("GET /api/v1/admin/config/rotation-status", g(http.HandlerFunc(h.rotationStatus)))
	mux.Handle("POST /api/v1/admin/config/rotate-secret", g(http.HandlerFunc(h.rotateSecret)))
	mux.Handle("GET /api/v1/admin/export/ledger", g(http.HandlerFunc(h.ExportLedger)))
	mux.HandleFunc("GET /api/v1/transactions/{id}/timeline", h.PublicTimeline)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
