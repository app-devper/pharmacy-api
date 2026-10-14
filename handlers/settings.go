package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type SettingsHandler struct{ dbm *db.Manager }

func NewSettingsHandler(d *db.Manager) *SettingsHandler { return &SettingsHandler{dbm: d} }

const settingsKey = db.SettingsKey

// loadStockSettings is the tenant's effective stock thresholds
// (db.StoreSettings decides the fallbacks).
func loadStockSettings(ctx context.Context, mdb *db.MongoDB) models.StockSettings {
	return mdb.StoreSettings(ctx).Stock
}

// Get returns the settings the shop runs on (db.StoreSettings): the built-in
// defaults until the shop first saves. Reading never writes.
// GET /api/pharmacy/v1/settings
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	jsonOK(w, mdb.StoreSettings(ctx))
}

// Update stores an edit as models.SettingsInput.Accept decides. ADMIN or SUPER only.
// PUT /api/pharmacy/v1/settings
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var input models.SettingsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	s, err := input.Accept()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	s.UpdatedAt = time.Now()
	update := bson.M{
		"$set": bson.M{
			"key":        settingsKey,
			"store":      s.Store,
			"receipt":    s.Receipt,
			"stock":      s.Stock,
			"pharmacist": s.Pharmacist,
			"ky":         s.KY,
			"timezone":   s.Timezone,
			"updated_at": s.UpdatedAt,
		},
	}
	if _, err := mdb.Settings().UpdateOne(ctx,
		bson.M{"key": settingsKey},
		update,
		options.UpdateOne().SetUpsert(true),
	); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, s)
}
