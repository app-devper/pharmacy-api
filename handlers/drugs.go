package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

func (h *DrugHandler) Add(w http.ResponseWriter, r *http.Request) {
	var input models.DrugInput
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

	tz := mdb.Timezone(ctx)
	drug, createLot, err := catalog.NewDrug(input, time.Now().In(tz), tz)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		res, err := mdb.Drugs().InsertOne(txCtx, drug)
		if err != nil {
			return err
		}
		drug.ID = res.InsertedID.(bson.ObjectID)
		if createLot == nil {
			return nil
		}

		lot := *createLot
		lot.DrugID = drug.ID
		lot.DrugName = drug.Name
		if lot.CostPrice == nil {
			lotCost := drug.CostPrice
			lot.CostPrice = &lotCost
		}
		if lot.SellPrice == nil {
			lotPrice := drug.SellPrice
			lot.SellPrice = &lotPrice
		}

		if err := inventory.OpenStock(txCtx, mdb, lot); err != nil {
			return fmt.Errorf("create lot failed: %w", err)
		}
		return nil
	}); err != nil {
		if db.IsDuplicateKey(err) {
			jsonError(w, "บาร์โค้ดนี้มีอยู่ในระบบแล้ว", http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, drug)
}

func (h *DrugHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.DrugUpdate
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	input, err = catalog.Edit(input)
	if err != nil {
		writeCommandError(w, err)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	res, err := mdb.Drugs().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"name":         input.Name,
			"generic_name": input.GenericName,
			"type":         input.Type,
			"strength":     input.Strength,
			"barcode":      input.Barcode,
			"price":        input.SellPrice, // bson key = "price", NOT "sell_price"
			"cost_price":   input.CostPrice,
			"min_stock":    input.MinStock,
			"reg_no":       input.RegNo,
			"unit":         input.Unit,
			"report_types": input.ReportTypes,
			"alt_units":    input.AltUnits,
			"prices":       input.Prices,
		}},
	)
	if err != nil {
		if db.IsDuplicateKey(err) {
			jsonError(w, "บาร์โค้ดนี้มีอยู่ในระบบแล้ว", http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		jsonError(w, "drug not found", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// BulkImportRowError describes a single failed row in a bulk import.
type BulkImportRowError struct {
	Row     int    `json:"row"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// BulkImportResult is the response for POST /drugs/bulk.
type BulkImportResult struct {
	Imported int                  `json:"imported"`
	Errors   []BulkImportRowError `json:"errors"`
}

// BulkImport accepts a JSON array of DrugInput, inserts each one individually,
// and returns a per-row error report rather than failing the whole batch.
// POST /api/pharmacy/v1/drugs/bulk  (ADMIN+)
func (h *DrugHandler) BulkImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Drugs []models.DrugInput `json:"drugs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(input.Drugs) == 0 {
		jsonError(w, "drugs is required", http.StatusBadRequest)
		return
	}
	if len(input.Drugs) > 1000 {
		jsonError(w, "ไม่เกิน 1,000 รายการต่อครั้ง", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}

	bulkCtx, bulkCancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer bulkCancel()

	result := BulkImportResult{Errors: []BulkImportRowError{}}
	for i, inp := range input.Drugs {
		row := i + 2 // row 1 = header in the Excel sheet
		if strings.TrimSpace(inp.Name) == "" {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: "-", Message: "ชื่อยาห้ามว่าง"})
			continue
		}
		drug, err := catalog.ImportedDrug(inp, time.Now())
		if err != nil {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: inp.Name, Message: err.Error()})
			continue
		}
		// Stock imported without lot data goes into an OPENING lot, so the
		// drug's stock and lots agree (ADR-0007).
		ctx, cancel := context.WithTimeout(bulkCtx, 5*time.Second)
		err = mdb.WithTransaction(ctx, func(txCtx context.Context) error {
			res, err := mdb.Drugs().InsertOne(txCtx, drug)
			if err != nil {
				return err
			}
			drug.ID = res.InsertedID.(bson.ObjectID)
			if drug.Stock <= 0 {
				return nil
			}
			return inventory.OpenStock(txCtx, mdb, inventory.OpeningLot(drug))
		})
		cancel()
		if err != nil {
			msg := "บันทึกไม่สำเร็จ"
			if db.IsDuplicateKey(err) {
				msg = "บาร์โค้ดนี้มีอยู่ในระบบแล้ว"
			}
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: inp.Name, Message: msg})
			continue
		}
		result.Imported++
	}
	jsonOK(w, result)
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
