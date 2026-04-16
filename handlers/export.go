package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/pdf"
)

type ExportHandler struct{ db *db.MongoDB }

func NewExportHandler(d *db.MongoDB) *ExportHandler { return &ExportHandler{db: d} }

func (h *ExportHandler) Export(w http.ResponseWriter, r *http.Request) {
	form := chi.URLParam(r, "form")
	month := r.URL.Query().Get("month")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	filter := bson.M{}
	if month != "" {
		filter = bson.M{"date": bson.M{"$regex": "^" + month}}
	}
	sortOpt := options.Find().SetSort(bson.D{{Key: "date", Value: 1}})

	filename := fmt.Sprintf("%s-%s.pdf", form, month)

	var buf interface{ Bytes() []byte }
	var err error

	switch form {
	case "ky9":
		cur, e := h.db.Ky9().Find(ctx, filter, sortOpt)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		var rows []models.Ky9
		cur.All(ctx, &rows)
		b, e := pdf.GenerateKy9(rows, month)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Write(b.Bytes())
		return

	case "ky10":
		cur, e := h.db.Ky10().Find(ctx, filter, sortOpt)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		var rows []models.Ky10
		cur.All(ctx, &rows)
		b, e := pdf.GenerateKy10(rows, month)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Write(b.Bytes())
		return

	case "ky11":
		cur, e := h.db.Ky11().Find(ctx, filter, sortOpt)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		var rows []models.Ky11
		cur.All(ctx, &rows)
		b, e := pdf.GenerateKy11(rows, month)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Write(b.Bytes())
		return

	case "ky12":
		cur, e := h.db.Ky12().Find(ctx, filter, sortOpt)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		var rows []models.Ky12
		cur.All(ctx, &rows)
		b, e := pdf.GenerateKy12(rows, month)
		if e != nil {
			jsonError(w, e.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Write(b.Bytes())
		return

	default:
		_ = buf
		_ = err
		jsonError(w, "unknown form: "+form, http.StatusBadRequest)
	}
}
