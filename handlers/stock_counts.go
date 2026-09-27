package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type StockCountHandler struct{ dbm *db.Manager }

func NewStockCountHandler(d *db.Manager) *StockCountHandler {
	return &StockCountHandler{dbm: d}
}

func (h *StockCountHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int64(20)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && v > 0 && v <= 100 {
		limit = v
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.StockCounts().Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var counts []models.StockCount
	if err := cur.All(ctx, &counts); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if counts == nil {
		counts = []models.StockCount{}
	}
	jsonOK(w, counts)
}

// Create records a stock count through the inventory module.
func (h *StockCountHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.StockCountInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	count, err := inventory.Count(ctx, mdb, input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, count)
}
