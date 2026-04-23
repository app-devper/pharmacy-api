package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type DrugLotHandler struct{ dbm *db.Manager }

func NewDrugLotHandler(d *db.Manager) *DrugLotHandler { return &DrugLotHandler{dbm: d} }

// ListLots returns all lots for a drug, sorted by expiry_date ASC (FEFO order).
func (h *DrugLotHandler) ListLots(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	drugOID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid drug id", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.DrugLots().Find(ctx,
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

	expiry, err := time.ParseInLocation("2006-01-02", input.ExpiryDate, time.Local)
	if err != nil {
		jsonError(w, "expiry_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	importDate := time.Now()
	if input.ImportDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", input.ImportDate, time.Local)
		if err != nil {
			jsonError(w, "import_date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		importDate = parsed
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var drug models.Drug
	if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugOID}).Decode(&drug); err != nil {
		jsonError(w, "drug not found", http.StatusNotFound)
		return
	}

	lot := models.DrugLot{
		DrugID:     drugOID,
		DrugName:   drug.Name,
		LotNumber:  input.LotNumber,
		ExpiryDate: expiry,
		ImportDate: importDate,
		CostPrice:  input.CostPrice,
		SellPrice:  input.SellPrice,
		Quantity:   input.Quantity,
		Remaining:  input.Quantity, // starts equal to quantity
		CreatedAt:  time.Now(),
	}

	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		res, err := mdb.DrugLots().InsertOne(txCtx, lot)
		if err != nil {
			return err
		}
		lot.ID = res.InsertedID.(bson.ObjectID)

		updateRes, err := mdb.Drugs().UpdateOne(txCtx,
			bson.M{"_id": drugOID},
			bson.M{"$inc": bson.M{"stock": input.Quantity}},
		)
		if err != nil {
			return err
		}
		if updateRes.MatchedCount == 0 {
			return mongo.ErrNoDocuments
		}
		return nil
	}); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "drug not found", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, lot)
}

// Expiring returns lots that still have remaining stock, filtered by expiry window.
// GET /api/pharmacy/v1/lots/expiring?days=60         — lots expiring within N days (default from Settings, includes already-expired)
// GET /api/pharmacy/v1/lots/expiring?expired_only=true — only lots whose expiry_date is already in the past
func (h *DrugLotHandler) Expiring(w http.ResponseWriter, r *http.Request) {
	expiredOnly := r.URL.Query().Get("expired_only") == "true"

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Default window comes from tenant settings; `?days=N` overrides per-request.
	days := loadStockSettings(ctx, mdb).ExpiringDays
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}

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

	cur, err := mdb.DrugLots().Find(ctx,
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

	result := make([]models.ExpiringLotItem, 0, len(lots))
	for _, l := range lots {
		daysLeft := int(l.ExpiryDate.Sub(now).Hours() / 24)
		result = append(result, models.ExpiringLotItem{
			ID:         l.ID,
			DrugID:     l.DrugID,
			DrugName:   l.DrugName,
			LotNumber:  l.LotNumber,
			ExpiryDate: l.ExpiryDate,
			Remaining:  l.Remaining,
			DaysLeft:   daysLeft,
		})
	}
	jsonOK(w, result)
}

// WriteoffLots bulk-deletes a set of lots and decrements each drug's stock accordingly.
// POST /api/pharmacy/v1/lots/writeoff   body: {"lot_ids": ["<hex>", ...]}
func (h *DrugLotHandler) WriteoffLots(w http.ResponseWriter, r *http.Request) {
	var input struct {
		LotIDs []string `json:"lot_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.LotIDs) == 0 {
		jsonError(w, "lot_ids required", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	writtenOff := 0
	failures := make([]map[string]string, 0)
	for _, rawID := range input.LotIDs {
		lotOID, err := bson.ObjectIDFromHex(rawID)
		if err != nil {
			failures = append(failures, map[string]string{
				"lot_id": rawID,
				"error":  "invalid lot id",
			})
			continue
		}

		if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
			var lot models.DrugLot
			if err := mdb.DrugLots().FindOne(txCtx, bson.M{"_id": lotOID}).Decode(&lot); err != nil {
				return err
			}

			res, err := mdb.DrugLots().DeleteOne(txCtx, bson.M{"_id": lotOID})
			if err != nil {
				return err
			}
			if res.DeletedCount == 0 {
				return fmt.Errorf("lot already removed")
			}

			if lot.Remaining > 0 {
				updateRes, err := mdb.Drugs().UpdateOne(txCtx,
					bson.M{"_id": lot.DrugID},
					bson.M{"$inc": bson.M{"stock": -lot.Remaining}},
				)
				if err != nil {
					return err
				}
				if updateRes.MatchedCount == 0 {
					return mongo.ErrNoDocuments
				}
			}

			_, err = mdb.LotWriteoffs().InsertOne(txCtx, models.LotWriteoff{
				ID:         bson.NewObjectID(),
				DrugID:     lot.DrugID,
				DrugName:   lot.DrugName,
				LotNumber:  lot.LotNumber,
				ExpiryDate: lot.ExpiryDate,
				Qty:        lot.Remaining,
				CreatedAt:  time.Now(),
			})
			return err
		}); err != nil {
			msg := err.Error()
			if errors.Is(err, mongo.ErrNoDocuments) {
				msg = "lot not found"
			}
			failures = append(failures, map[string]string{
				"lot_id": rawID,
				"error":  msg,
			})
			continue
		}

		writtenOff++
	}

	status := http.StatusOK
	if len(failures) > 0 {
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"written_off": writtenOff,
		"failed":      failures,
	})
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

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Fetch lot to get remaining quantity before deleting
	var lot models.DrugLot
	err = mdb.DrugLots().FindOne(ctx, bson.M{"_id": lotOID, "drug_id": drugOID}).Decode(&lot)
	if err != nil {
		jsonError(w, "lot not found", http.StatusNotFound)
		return
	}

	// Delete lot + decrement stock atomically
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		res, err := mdb.DrugLots().DeleteOne(txCtx, bson.M{"_id": lotOID})
		if err != nil {
			return err
		}
		if res.DeletedCount == 0 {
			return mongo.ErrNoDocuments
		}
		if lot.Remaining > 0 {
			updateRes, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": drugOID},
				bson.M{"$inc": bson.M{"stock": -lot.Remaining}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "lot not found", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}
