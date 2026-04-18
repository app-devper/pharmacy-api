package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type SettingsHandler struct{ dbm *db.Manager }

func NewSettingsHandler(d *db.Manager) *SettingsHandler { return &SettingsHandler{dbm: d} }

const settingsKey = "singleton"

// loadStockSettings fetches the tenant's stock config. On any error (missing
// document, DB down, decode failure) it silently falls back to built-in
// defaults so calling handlers never break.
func loadStockSettings(ctx context.Context, mdb *db.MongoDB) models.StockSettings {
	var s models.Settings
	if err := mdb.Settings().FindOne(ctx, bson.M{"key": settingsKey}).Decode(&s); err != nil {
		return models.DefaultSettings().Stock
	}
	// Fill zero values with defaults so older docs without the stock section still work.
	if s.Stock.LowStockThreshold == 0 {
		s.Stock.LowStockThreshold = models.DefaultLowStockThreshold
	}
	if s.Stock.ReorderDays == 0 {
		s.Stock.ReorderDays = models.DefaultReorderDays
	}
	if s.Stock.ReorderLookahead == 0 {
		s.Stock.ReorderLookahead = models.DefaultReorderLookahead
	}
	if s.Stock.ExpiringDays == 0 {
		s.Stock.ExpiringDays = models.DefaultExpiringDays
	}
	return s.Stock
}

// Get returns the tenant's settings document, creating a default if none exists.
// GET /api/settings
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var s models.Settings
	err = mdb.Settings().FindOne(ctx, bson.M{"key": settingsKey}).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		s = models.DefaultSettings()
		s.UpdatedAt = time.Now()
		_, _ = mdb.Settings().InsertOne(ctx, s)
		jsonOK(w, s)
		return
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, s)
}

// Update upserts the settings document. ADMIN or SUPER only.
// PUT /api/settings
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var input models.SettingsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	// Trim whitespace on every string so blank-looking inputs are stored empty.
	input.Store.Name = strings.TrimSpace(input.Store.Name)
	input.Store.Address = strings.TrimSpace(input.Store.Address)
	input.Store.Phone = strings.TrimSpace(input.Store.Phone)
	input.Store.TaxID = strings.TrimSpace(input.Store.TaxID)
	input.Receipt.Header = strings.TrimSpace(input.Receipt.Header)
	input.Receipt.Footer = strings.TrimSpace(input.Receipt.Footer)
	input.Pharmacist.Name = strings.TrimSpace(input.Pharmacist.Name)
	input.Pharmacist.LicenseNo = strings.TrimSpace(input.Pharmacist.LicenseNo)
	input.KY.DefaultBuyerAddress = strings.TrimSpace(input.KY.DefaultBuyerAddress)

	if input.Store.Name == "" {
		jsonError(w, "ชื่อร้านห้ามว่าง", http.StatusBadRequest)
		return
	}
	switch input.Receipt.PaperWidth {
	case "58", "80":
		// ok
	case "":
		input.Receipt.PaperWidth = "58"
	default:
		jsonError(w, "paper_width ต้องเป็น 58 หรือ 80 เท่านั้น", http.StatusBadRequest)
		return
	}

	// Stock defaults — clamp to sane ranges; 0 falls back to built-in default.
	if input.Stock.LowStockThreshold < 0 {
		jsonError(w, "low_stock_threshold ต้องไม่ติดลบ", http.StatusBadRequest)
		return
	}
	if input.Stock.LowStockThreshold == 0 {
		input.Stock.LowStockThreshold = models.DefaultLowStockThreshold
	}
	if input.Stock.ReorderDays <= 0 {
		input.Stock.ReorderDays = models.DefaultReorderDays
	}
	if input.Stock.ReorderDays > 365 {
		input.Stock.ReorderDays = 365
	}
	if input.Stock.ReorderLookahead <= 0 {
		input.Stock.ReorderLookahead = models.DefaultReorderLookahead
	}
	if input.Stock.ReorderLookahead > 180 {
		input.Stock.ReorderLookahead = 180
	}
	if input.Stock.ExpiringDays <= 0 {
		input.Stock.ExpiringDays = models.DefaultExpiringDays
	}
	if input.Stock.ExpiringDays > 365 {
		input.Stock.ExpiringDays = 365
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"key":        settingsKey,
			"store":      input.Store,
			"receipt":    input.Receipt,
			"stock":      input.Stock,
			"pharmacist": input.Pharmacist,
			"ky":         input.KY,
			"updated_at": now,
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

	jsonOK(w, models.Settings{
		Key:        settingsKey,
		Store:      input.Store,
		Receipt:    input.Receipt,
		Stock:      input.Stock,
		Pharmacist: input.Pharmacist,
		KY:         input.KY,
		UpdatedAt:  now,
	})
}
