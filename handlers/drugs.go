package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/catalog"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/reporting"
)

type DrugHandler struct{ dbm *db.Manager }

func NewDrugHandler(d *db.Manager) *DrugHandler { return &DrugHandler{dbm: d} }

func (h *DrugHandler) List(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	findOpts := options.Find().SetSort(bson.D{{Key: "name", Value: 1}})
	// ?fields=compact → return only fields needed by SellPage/DrugGrid to cut payload ~60%.
	// Omit this param (or use any other value) to get the full Drug document.
	if r.URL.Query().Get("fields") == "compact" {
		findOpts.SetProjection(bson.M{
			"_id": 1, "name": 1, "price": 1, "cost_price": 1,
			"stock": 1, "barcode": 1, "reg_no": 1, "unit": 1, "report_types": 1,
		})
	}
	cur, err := mdb.Drugs().Find(ctx, bson.M{}, findOpts)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var drugs []models.Drug
	if err := cur.All(ctx, &drugs); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if drugs == nil {
		drugs = []models.Drug{}
	}
	// Attach next-FEFO lot summary per drug so the client can surface
	// "next expiry" in the UI and snapshot the expected lot at checkout.
	attachNextLots(ctx, mdb, drugs)
	jsonOK(w, drugs)
}

// attachNextLots decorates drugs in place with NextLot, the lot the next
// sale takes from (inventory decides). Errors are swallowed: a missing lot
// summary should never block the drug list from returning.
func attachNextLots(ctx context.Context, mdb *db.MongoDB, drugs []models.Drug) {
	if len(drugs) == 0 {
		return
	}
	next, err := inventory.NextLots(ctx, mdb)
	if err != nil {
		return
	}
	for i := range drugs {
		if lot, ok := next[drugs[i].ID]; ok {
			drugs[i].NextLot = &lot
		}
	}
}

// Add registers a drug and its opening lot (catalog.Register).
func (h *DrugHandler) Add(w http.ResponseWriter, r *http.Request) {
	var input models.DrugInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	serve(h.dbm, w, r, 5*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return catalog.Register(ctx, mdb, input)
	})
}

// Update stores an edit of a drug (catalog.Update).
func (h *DrugHandler) Update(w http.ResponseWriter, r *http.Request) {
	var input models.DrugUpdate
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	serve(h.dbm, w, r, 5*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		if err := catalog.Update(ctx, mdb, chi.URLParam(r, "id"), input); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	})
}

// BulkImport records each drug on its own and reports the rows that were not
// recorded, rather than failing the whole batch (catalog.Import).
// POST /api/pharmacy/v1/drugs/bulk  (ADMIN+)
func (h *DrugHandler) BulkImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Drugs []models.DrugInput `json:"drugs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	serve(h.dbm, w, r, 30*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return catalog.Import(ctx, mdb, input.Drugs)
	})
}

// LowStock returns drugs where min_stock > 0 AND stock <= min_stock, sorted by stock ASC.
// GET /api/pharmacy/v1/drugs/low-stock
func (h *DrugHandler) LowStock(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Low stock = nearly out (stock > 0 and <= min_stock, or the tenant's
	// default threshold); stock <= 0 is out of stock. inventory owns the rule.
	drugs, err := inventory.LowStock(ctx, mdb, loadStockSettings(ctx, mdb).LowStockThreshold)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, drugs)
}

// ReorderSuggestions — sales-driven reorder advice (reporting.Reorder).
//
// Query params (defaults from tenant settings):
//   - days       lookback window for averaging sales (max 365)
//   - lookahead  target cover days (max 180)
//
// GET /api/pharmacy/v1/drugs/reorder-suggestions
func (h *DrugHandler) ReorderSuggestions(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	stockCfg := loadStockSettings(ctx, mdb)
	days := intParam(r, "days", stockCfg.ReorderDays, 365)
	lookahead := intParam(r, "lookahead", stockCfg.ReorderLookahead, 180)
	out, err := reporting.Reorder(ctx, mdb, time.Now(), days, lookahead)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, out)
}
