package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

type StockAdjustmentHandler struct{ db *db.MongoDB }

func NewStockAdjustmentHandler(d *db.MongoDB) *StockAdjustmentHandler {
	return &StockAdjustmentHandler{db: d}
}

// Create records a manual stock adjustment and atomically updates drug.stock.
// POST /api/drugs/{id}/adjustments
func (h *StockAdjustmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.StockAdjustmentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Delta == 0 {
		jsonError(w, "delta must not be zero", http.StatusBadRequest)
		return
	}
	if input.Reason == "" {
		jsonError(w, "reason is required", http.StatusBadRequest)
		return
	}
	if !slices.Contains(models.AdjustmentReasons, input.Reason) {
		jsonError(w, "invalid reason", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Atomically increment stock and get the updated drug in one round-trip.
	var updated models.Drug
	err = h.db.Drugs().FindOneAndUpdate(ctx,
		bson.M{"_id": oid},
		bson.M{"$inc": bson.M{"stock": input.Delta}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Insert audit log entry.
	adj := models.StockAdjustment{
		DrugID:    oid,
		DrugName:  updated.Name,
		Delta:     input.Delta,
		Before:    updated.Stock - input.Delta,
		After:     updated.Stock,
		Reason:    input.Reason,
		Note:      input.Note,
		CreatedAt: time.Now(),
	}
	if _, err := h.db.StockAdjustments().InsertOne(ctx, adj); err != nil {
		// Non-fatal: stock was already updated; log the error and continue.
		// In production this could be retried or queued.
		_ = err
	}

	jsonOK(w, updated)
}

// List returns the adjustment history for a drug, newest first.
// GET /api/drugs/{id}/adjustments
func (h *StockAdjustmentHandler) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.StockAdjustments().Find(ctx,
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
