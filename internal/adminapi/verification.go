package adminapi

import (
	"net/http"

	"github.com/IDEA-Amrita/paystable/internal/verification"
)

func (h *Handler) verificationDemo(w http.ResponseWriter, _ *http.Request) {
	report, err := verification.RunDemo()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}
