package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/pdf"
)

type ExportHandler struct{ dbm *db.Manager }

func NewExportHandler(d *db.Manager) *ExportHandler { return &ExportHandler{dbm: d} }

// printRegister reads a register oldest first, as it is printed, and renders it.
func printRegister[T compliance.Row](ctx context.Context, mdb *db.MongoDB, month string, render func([]T, string) (*bytes.Buffer, error)) (*bytes.Buffer, error) {
	rows, err := compliance.Month[T](ctx, mdb, month, compliance.OldestFirst)
	if err != nil {
		return nil, err
	}
	return render(rows, month)
}

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

	var b *bytes.Buffer
	switch form {
	case compliance.Ky9:
		b, err = printRegister[models.Ky9](ctx, mdb, month, pdf.GenerateKy9)
	case compliance.Ky10:
		b, err = printRegister[models.Ky10](ctx, mdb, month, pdf.GenerateKy10)
	case compliance.Ky11:
		b, err = printRegister[models.Ky11](ctx, mdb, month, pdf.GenerateKy11)
	case compliance.Ky12:
		b, err = printRegister[models.Ky12](ctx, mdb, month, pdf.GenerateKy12)
	}
	if err != nil {
		writeCommandError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-%s.pdf\"", form, month))
	w.Write(b.Bytes())
}
