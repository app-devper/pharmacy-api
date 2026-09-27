package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type StockAdjustmentHandler struct{ dbm *db.Manager }

func NewStockAdjustmentHandler(d *db.Manager) *StockAdjustmentHandler {
	return &StockAdjustmentHandler{dbm: d}
}

// Create records a manual stock adjustment through the inventory module.
// POST /api/pharmacy/v1/drugs/{id}/adjustments
func (h *StockAdjustmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.StockAdjustmentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	updated, err := inventory.Adjust(ctx, mdb, chi.URLParam(r, "id"), input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, updated)
}

// List returns the adjustment history for a drug, newest first.
// GET /api/pharmacy/v1/drugs/{id}/adjustments
func (h *StockAdjustmentHandler) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.StockAdjustments().Find(ctx,
		bson.M{"drug_id": oid},
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(100),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var list []models.StockAdjustment
	if err := cur.All(ctx, &list); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []models.StockAdjustment{}
	}
	jsonOK(w, list)
}
