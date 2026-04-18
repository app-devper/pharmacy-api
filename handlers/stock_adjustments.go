package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type StockAdjustmentHandler struct{ dbm *db.Manager }

func NewStockAdjustmentHandler(d *db.Manager) *StockAdjustmentHandler {
	return &StockAdjustmentHandler{dbm: d}
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

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Apply stock change and audit insert atomically.
	var updated models.Drug
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		filter := bson.M{"_id": oid}
		if input.Delta < 0 {
			filter["stock"] = bson.M{"$gte": -input.Delta}
		}
		if err := mdb.Drugs().FindOneAndUpdate(txCtx,
			filter,
			bson.M{"$inc": bson.M{"stock": input.Delta}},
			options.FindOneAndUpdate().SetReturnDocument(options.After),
		).Decode(&updated); err != nil {
			return err
		}

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
		_, err := mdb.StockAdjustments().InsertOne(txCtx, adj)
		return err
	}); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "drug not found or insufficient stock", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
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
