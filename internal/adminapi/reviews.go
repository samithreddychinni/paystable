package adminapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) reviews(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT h.txn_id, h.gateway, h.status, h.amount, h.updated_at,
		       mr.resolution, mr.operator, mr.note, mr.resolved_at,
		       o.id, o.status
		FROM holds h
		LEFT JOIN manual_reviews mr ON mr.txn_id = h.txn_id
		LEFT JOIN outbox o ON o.idempotency_key = 'evt_' || h.txn_id || '_MANUAL_REVIEW'
		WHERE h.status IN ('MISMATCH', 'INDETERMINATE')
		ORDER BY h.updated_at ASC`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = rows.Close() }()

	type review struct {
		TxnID         string     `json:"txn_id"`
		Gateway       string     `json:"gateway"`
		Status        string     `json:"status"`
		Amount        int64      `json:"amount"`
		DetectedAt    time.Time  `json:"detected_at"`
		Resolution    *string    `json:"resolution,omitempty"`
		Operator      *string    `json:"operator,omitempty"`
		Note          *string    `json:"note,omitempty"`
		ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
		DeliveryID    *int64     `json:"delivery_id,omitempty"`
		DeliveryState *string    `json:"delivery_state,omitempty"`
	}

	data := []review{}
	for rows.Next() {
		var review review
		var resolution, operator, note, deliveryState sql.NullString
		var resolvedAt sql.NullTime
		var deliveryID sql.NullInt64
		if err := rows.Scan(&review.TxnID, &review.Gateway, &review.Status, &review.Amount, &review.DetectedAt,
			&resolution, &operator, &note, &resolvedAt, &deliveryID, &deliveryState); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if resolution.Valid {
			review.Resolution, review.Operator, review.Note = &resolution.String, &operator.String, &note.String
			review.ResolvedAt = &resolvedAt.Time
		}
		if deliveryID.Valid {
			review.DeliveryID, review.DeliveryState = &deliveryID.Int64, &deliveryState.String
		}
		data = append(data, review)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": len(data)})
}

func (h *Handler) resolveReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Resolution string `json:"resolution"`
		Operator   string `json:"operator"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Resolution = strings.TrimSpace(req.Resolution)
	req.Operator = strings.TrimSpace(req.Operator)
	req.Note = strings.TrimSpace(req.Note)
	if req.Resolution != "confirmed" && req.Resolution != "failed" && req.Resolution != "no_action" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resolution must be confirmed, failed, or no_action"})
		return
	}
	if req.Operator == "" || len(req.Operator) > 128 || req.Note == "" || len(req.Note) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operator and note are required"})
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = tx.Rollback() }()

	txnID := r.PathValue("id")
	var status, gateway, currency string
	var amount int64
	var metadata json.RawMessage
	if err := tx.QueryRowContext(r.Context(), `
		SELECT status, gateway, amount, currency, metadata
		FROM holds WHERE txn_id = $1 FOR UPDATE`, txnID).
		Scan(&status, &gateway, &amount, &currency, &metadata); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "transaction not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if status != "MISMATCH" && status != "INDETERMINATE" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only MISMATCH and INDETERMINATE transactions can be reviewed"})
		return
	}

	var existingResolution, existingOperator, existingNote string
	err = tx.QueryRowContext(r.Context(), `
		SELECT resolution, operator, note FROM manual_reviews WHERE txn_id = $1`, txnID).
		Scan(&existingResolution, &existingOperator, &existingNote)
	if err == nil {
		if existingResolution != req.Resolution || existingOperator != req.Operator || existingNote != req.Note {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "transaction already has a different review decision"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true})
		return
	}
	if err != sql.ErrNoRows {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	resolvedAt := time.Now().UTC()
	idempotencyKey := ""
	var deliveryID int64
	if req.Resolution != "no_action" {
		finalStatus := strings.ToUpper(req.Resolution)
		payload, err := json.Marshal(map[string]any{
			"txn_id":            txnID,
			"event":             "transaction." + req.Resolution,
			"status":            finalStatus,
			"amount":            amount,
			"currency":          currency,
			"gateway":           gateway,
			"verified_at":       resolvedAt.Format(time.RFC3339),
			"metadata":          metadata,
			"reason":            "manual_review",
			"resolution_source": "manual_review",
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("marshal callback payload: %v", err)})
			return
		}
		idempotencyKey = "evt_" + txnID + "_MANUAL_REVIEW"
		if err := tx.QueryRowContext(r.Context(), `
			INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, next_attempt_at)
			VALUES ($1, $2, $3, $4, now()) RETURNING id`,
			txnID, "transaction."+finalStatus, payload, idempotencyKey).Scan(&deliveryID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO manual_reviews (txn_id, resolution, operator, note)
		VALUES ($1, $2, $3, $4)`, txnID, req.Resolution, req.Operator, req.Note); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	detailFields := map[string]any{
		"resolution": req.Resolution,
		"operator":   req.Operator,
		"note":       req.Note,
	}
	if deliveryID != 0 {
		detailFields["delivery_id"] = deliveryID
		detailFields["idempotency_key"] = idempotencyKey
	}
	detail, err := json.Marshal(detailFields)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("marshal review detail: %v", err)})
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO ledger (txn_id, event_type, source, from_status, to_status, detail)
		VALUES ($1, 'manual_review', 'operator', $2, $2, $3)`, txnID, status, detail); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response := map[string]any{"success": true}
	if deliveryID != 0 {
		response["delivery_id"] = deliveryID
		response["delivery_state"] = "pending"
	}
	writeJSON(w, http.StatusOK, response)
}
