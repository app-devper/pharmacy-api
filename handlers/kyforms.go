package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

// KyHandler translates HTTP for the KY registers; what a valid row is and
// how registers are read belong to the compliance module.
type KyHandler struct{ dbm *db.Manager }

func NewKyHandler(d *db.Manager) *KyHandler { return &KyHandler{dbm: d} }

// ky runs fn against the caller's tenant and writes its answer; a refusal
// maps to its status.
func (h *KyHandler) ky(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, mdb *db.MongoDB) (any, error)) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := fn(ctx, mdb)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, out)
}

// listKy serves GET /kyN?month=YYYY-MM, newest first.
func listKy[T compliance.Row](h *KyHandler, w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	h.ky(w, r, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return compliance.Month[T](ctx, mdb, month, compliance.NewestFirst)
	})
}

// addKy serves POST /kyN: decode the input and record it.
func addKy[In any](h *KyHandler, w http.ResponseWriter, r *http.Request, record func(ctx context.Context, mdb *db.MongoDB, in In) (any, error)) {
	var in In
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	h.ky(w, r, func(ctx context.Context, mdb *db.MongoDB) (any, error) { return record(ctx, mdb, in) })
}

func (h *KyHandler) ListKy9(w http.ResponseWriter, r *http.Request)  { listKy[models.Ky9](h, w, r) }
func (h *KyHandler) ListKy10(w http.ResponseWriter, r *http.Request) { listKy[models.Ky10](h, w, r) }
func (h *KyHandler) ListKy11(w http.ResponseWriter, r *http.Request) { listKy[models.Ky11](h, w, r) }
func (h *KyHandler) ListKy12(w http.ResponseWriter, r *http.Request) { listKy[models.Ky12](h, w, r) }

func (h *KyHandler) AddKy9(w http.ResponseWriter, r *http.Request) {
	addKy(h, w, r, func(ctx context.Context, mdb *db.MongoDB, in models.Ky9Input) (any, error) {
		row, err := compliance.RecordKy9(ctx, mdb, in, time.Now())
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": row.ID.Hex(), "total_value": row.TotalValue}, nil
	})
}

func (h *KyHandler) AddKy10(w http.ResponseWriter, r *http.Request) {
	addKy(h, w, r, func(ctx context.Context, mdb *db.MongoDB, in models.Ky10Input) (any, error) {
		row, err := compliance.RecordKy10(ctx, mdb, in, compliance.LoadDefaults(ctx, mdb), time.Now())
		return idOf(row.ID.Hex(), err)
	})
}

func (h *KyHandler) AddKy11(w http.ResponseWriter, r *http.Request) {
	addKy(h, w, r, func(ctx context.Context, mdb *db.MongoDB, in models.Ky11Input) (any, error) {
		row, err := compliance.RecordKy11(ctx, mdb, in, compliance.LoadDefaults(ctx, mdb), time.Now())
		return idOf(row.ID.Hex(), err)
	})
}

func (h *KyHandler) AddKy12(w http.ResponseWriter, r *http.Request) {
	addKy(h, w, r, func(ctx context.Context, mdb *db.MongoDB, in models.Ky12Input) (any, error) {
		row, err := compliance.RecordKy12(ctx, mdb, in, time.Now())
		return idOf(row.ID.Hex(), err)
	})
}

func idOf(id string, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

// BySale — GET /sales/{id}/ky: the KY rows recorded for a sale.
func (h *KyHandler) BySale(w http.ResponseWriter, r *http.Request) {
	saleID := strings.TrimSpace(chi.URLParam(r, "id"))
	if saleID == "" {
		jsonError(w, "sale id is required", http.StatusBadRequest)
		return
	}
	h.ky(w, r, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return compliance.BySale(ctx, mdb, saleID)
	})
}
