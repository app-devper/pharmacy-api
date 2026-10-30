package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
)

type ExportHandler struct{ dbm *db.Manager }

func NewExportHandler(d *db.Manager) *ExportHandler { return &ExportHandler{dbm: d} }

// Export — GET /export/{form}?month=YYYY-MM: the register as a PDF.
func (h *ExportHandler) Export(w http.ResponseWriter, r *http.Request) {
	form, ok := compliance.ParseForm(chi.URLParam(r, "form"))
	if !ok {
		jsonError(w, "unknown form: "+chi.URLParam(r, "form"), http.StatusBadRequest)
		return
	}
	month := r.URL.Query().Get("month")

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := compliance.Print(ctx, mdb, form, month)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-%s.pdf\"", form, month))
	w.Write(b.Bytes())
}
