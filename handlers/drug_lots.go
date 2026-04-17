package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

type DrugLotHandler struct{ db *db.MongoDB }

func NewDrugLotHandler(d *db.MongoDB) *DrugLotHandler { return &DrugLotHandler{db: d} }

// ListLots returns all lots for a drug, sorted by expiry_date ASC (FEFO order).
func (h *DrugLotHandler) ListLots(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	drugOID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid drug id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.DrugLots().Find(ctx,
		bson.M{"drug_id": drugOID},
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var lots []models.DrugLot
	if err := cur.All(ctx, &lots); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if lots == nil {
		lots = []models.DrugLot{}
	}
	jsonOK(w, lots)
}

// AddLot creates a new lot and increments drug.stock by lot.Quantity.
func (h *DrugLotHandler) AddLot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	drugOID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid drug id", http.StatusBadRequest)
		return
	}

	var input models.DrugLotInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.LotNumber == "" {
		jsonError(w, "lot_number is required", http.StatusBadRequest)
		return
	}
	if input.Quantity <= 0 {
		jsonError(w, "quantity must be > 0", http.StatusBadRequest)
		return
	}
	if input.ExpiryDate == "" {
		jsonError(w, "expiry_date is required", http.StatusBadRequest)
		return
	}

	expiry, err := time.Parse("2006-01-02", input.ExpiryDate)
	if err != nil {
		jsonError(w, "expiry_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	importDate := time.Now()
	if input.ImportDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ImportDate); err == nil {
			importDate = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	lot := models.DrugLot{
		DrugID:     drugOID,
		LotNumber:  input.LotNumber,
		ExpiryDate: expiry,
		ImportDate: importDate,
		CostPrice:  input.CostPrice,
		SellPrice:  input.SellPrice,
		Quantity:   input.Quantity,
		Remaining:  input.Quantity, // starts equal to quantity
		CreatedAt:  time.Now(),
	}

	res, err := h.db.DrugLots().InsertOne(ctx, lot)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lot.ID = res.InsertedID.(bson.ObjectID)

	// Atomically increment drug.stock by lot quantity
	_, err = h.db.Drugs().UpdateOne(ctx,
		bson.M{"_id": drugOID},
		bson.M{"$inc": bson.M{"stock": input.Quantity}},
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, lot)
}

// Expiring returns lots that still have remaining stock, filtered by expiry window.
// GET /api/lots/expiring?days=60         — lots expiring within N days (default 60, includes already-expired)
// GET /api/lots/expiring?expired_only=true — only lots whose expiry_date is already in the past
func (h *DrugLotHandler) Expiring(w http.ResponseWriter, r *http.Request) {
	days := 60
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}
	expiredOnly := r.URL.Query().Get("expired_only") == "true"

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	now := time.Now()

	var filter bson.M
	if expiredOnly {
		filter = bson.M{
			"expiry_date": bson.M{"$lt": now},
			"remaining":   bson.M{"$gt": 0},
		}
	} else {
		threshold := now.AddDate(0, 0, days)
		filter = bson.M{
			"expiry_date": bson.M{"$lte": threshold},
			"remaining":   bson.M{"$gt": 0},
		}
	}

	cur, err := h.db.DrugLots().Find(ctx,
		filter,
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var lots []models.DrugLot
	if err := cur.All(ctx, &lots); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(lots) == 0 {
		jsonOK(w, []models.ExpiringLotItem{})
		return
	}

	// Batch-fetch drug names for all unique DrugIDs
	drugIDSet := map[bson.ObjectID]struct{}{}
	for _, l := range lots {
		drugIDSet[l.DrugID] = struct{}{}
	}
	ids := make([]bson.ObjectID, 0, len(drugIDSet))
	for id := range drugIDSet {
		ids = append(ids, id)
	}
	drugCur, err := h.db.Drugs().Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"_id": 1, "name": 1}),
	)
	nameMap := map[bson.ObjectID]string{}
	if err == nil {
		var drugs []models.Drug
		drugCur.All(ctx, &drugs)
		drugCur.Close(ctx)
		for _, d := range drugs {
			nameMap[d.ID] = d.Name
		}
	}

	result := make([]models.ExpiringLotItem, 0, len(lots))
	for _, l := range lots {
		daysLeft := int(l.ExpiryDate.Sub(now).Hours() / 24)
		result = append(result, models.ExpiringLotItem{
			ID:         l.ID,
			DrugID:     l.DrugID,
			DrugName:   nameMap[l.DrugID],
			LotNumber:  l.LotNumber,
			ExpiryDate: l.ExpiryDate,
			Remaining:  l.Remaining,
			DaysLeft:   daysLeft,
		})
	}
	jsonOK(w, result)
}

// WriteoffLots bulk-deletes a set of lots and decrements each drug's stock accordingly.
// POST /api/lots/writeoff   body: {"lot_ids": ["<hex>", ...]}
func (h *DrugLotHandler) WriteoffLots(w http.ResponseWriter, r *http.Request) {
	var input struct {
		LotIDs []string `json:"lot_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.LotIDs) == 0 {
		jsonError(w, "lot_ids required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	writtenOff := 0
	for _, rawID := range input.LotIDs {
		lotOID, err := bson.ObjectIDFromHex(rawID)
		if err != nil {
			continue
		}

		var lot models.DrugLot
		if err := h.db.DrugLots().FindOneAndDelete(ctx, bson.M{"_id": lotOID}).Decode(&lot); err != nil {
			continue // not found or already deleted — skip
		}

		if lot.Remaining > 0 {
			h.db.Drugs().UpdateOne(ctx,
				bson.M{"_id": lot.DrugID},
				bson.M{"$inc": bson.M{"stock": -lot.Remaining}},
			)
		}

		// Audit log — lookup drug name then record the write-off
		var drug models.Drug
		h.db.Drugs().FindOne(ctx, bson.M{"_id": lot.DrugID}).Decode(&drug)
		h.db.LotWriteoffs().InsertOne(ctx, models.LotWriteoff{
			ID:         bson.NewObjectID(),
			DrugID:     lot.DrugID,
			DrugName:   drug.Name,
			LotNumber:  lot.LotNumber,
			ExpiryDate: lot.ExpiryDate,
			Qty:        lot.Remaining,
			CreatedAt:  time.Now(),
		})

		writtenOff++
	}

	jsonOK(w, map[string]int{"written_off": writtenOff})
}

// DeleteLot removes a lot and decrements drug.stock by lot.Remaining.
func (h *DrugLotHandler) DeleteLot(w http.ResponseWriter, r *http.Request) {
	drugID := chi.URLParam(r, "id")
	lotID := chi.URLParam(r, "lot_id")

	drugOID, err := bson.ObjectIDFromHex(drugID)
	if err != nil {
		jsonError(w, "invalid drug id", http.StatusBadRequest)
		return
	}
	lotOID, err := bson.ObjectIDFromHex(lotID)
	if err != nil {
		jsonError(w, "invalid lot id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Fetch lot to get remaining quantity before deleting
	var lot models.DrugLot
	err = h.db.DrugLots().FindOne(ctx, bson.M{"_id": lotOID, "drug_id": drugOID}).Decode(&lot)
	if err != nil {
		jsonError(w, "lot not found", http.StatusNotFound)
		return
	}

	// Delete the lot
	_, err = h.db.DrugLots().DeleteOne(ctx, bson.M{"_id": lotOID})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Decrement drug.stock by the remaining qty (not original quantity)
	if lot.Remaining > 0 {
		h.db.Drugs().UpdateOne(ctx,
			bson.M{"_id": drugOID},
			bson.M{"$inc": bson.M{"stock": -lot.Remaining}},
		)
	}

	jsonOK(w, map[string]bool{"ok": true})
}
