package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

// isMongoDuplicate returns true when err is a MongoDB duplicate-key (code 11000) error.
func isMongoDuplicate(err error) bool {
	var we mongo.WriteException
	if errors.As(err, &we) {
		for _, e := range we.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}

type DrugHandler struct{ dbm *db.Manager }

func NewDrugHandler(d *db.Manager) *DrugHandler { return &DrugHandler{dbm: d} }

func buildDrugCreatePayload(input models.DrugInput, now time.Time) (models.Drug, *models.DrugLot, error) {
	if input.Name == "" {
		return models.Drug{}, nil, errors.New("name is required")
	}
	if len(input.Name) > 255 {
		return models.Drug{}, nil, errors.New("name too long (max 255)")
	}
	if input.Type == "" {
		input.Type = "ยาสามัญ"
	}
	if input.Unit == "" {
		input.Unit = "ชิ้น"
	}
	if input.ReportTypes == nil {
		input.ReportTypes = []string{}
	}
	input.Barcode = strings.TrimSpace(input.Barcode)

	// Reject negative stock outright.
	if input.Stock < 0 {
		return models.Drug{}, nil, errors.New("stock must be >= 0")
	}
	initialStock := input.Stock
	// Enforce: stock > 0 requires create_lot so drug.stock is always backed by a real lot.
	if input.Stock > 0 && input.CreateLot == nil {
		return models.Drug{}, nil, errors.New("create_lot is required when stock > 0")
	}
	var createLot *models.DrugLot
	if input.CreateLot != nil {
		input.CreateLot.LotNumber = strings.TrimSpace(input.CreateLot.LotNumber)
		if input.CreateLot.LotNumber == "" {
			return models.Drug{}, nil, errors.New("create_lot.lot_number is required")
		}
		if input.CreateLot.Quantity <= 0 {
			return models.Drug{}, nil, errors.New("create_lot.quantity must be > 0")
		}
		if input.CreateLot.ExpiryDate == "" {
			return models.Drug{}, nil, errors.New("create_lot.expiry_date is required")
		}

		expiry, err := time.ParseInLocation("2006-01-02", input.CreateLot.ExpiryDate, time.Local)
		if err != nil {
			return models.Drug{}, nil, errors.New("create_lot.expiry_date must be YYYY-MM-DD")
		}
		// Expiry must be strictly after today (midnight-local).
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		if !expiry.After(today) {
			return models.Drug{}, nil, errors.New("create_lot.expiry_date must be in the future")
		}
		importDate := now
		if input.CreateLot.ImportDate != "" {
			parsed, err := time.ParseInLocation("2006-01-02", input.CreateLot.ImportDate, time.Local)
			if err != nil {
				return models.Drug{}, nil, errors.New("create_lot.import_date must be YYYY-MM-DD")
			}
			importDate = parsed
		}
		if input.Stock != 0 && input.Stock != input.CreateLot.Quantity {
			return models.Drug{}, nil, errors.New("stock must be 0 or equal create_lot.quantity when create_lot is provided")
		}
		initialStock = input.CreateLot.Quantity
		createLot = &models.DrugLot{
			DrugName:   input.Name,
			LotNumber:  input.CreateLot.LotNumber,
			ExpiryDate: expiry,
			ImportDate: importDate,
			CostPrice:  input.CreateLot.CostPrice,
			SellPrice:  input.CreateLot.SellPrice,
			Quantity:   input.CreateLot.Quantity,
			Remaining:  input.CreateLot.Quantity,
			CreatedAt:  now,
		}
	}

	drug := models.Drug{
		Name:        input.Name,
		GenericName: input.GenericName,
		Type:        input.Type,
		Strength:    input.Strength,
		Barcode:     input.Barcode,
		SellPrice:   input.SellPrice,
		CostPrice:   input.CostPrice,
		Stock:       initialStock,
		MinStock:    input.MinStock,
		RegNo:       input.RegNo,
		Unit:        input.Unit,
		ReportTypes: input.ReportTypes,
		CreatedAt:   now,
	}
	return drug, createLot, nil
}

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
	jsonOK(w, drugs)
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

	drug, createLot, err := buildDrugCreatePayload(input, time.Now())
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
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

		if _, err := mdb.DrugLots().InsertOne(txCtx, lot); err != nil {
			return fmt.Errorf("create lot failed: %w", err)
		}
		return nil
	}); err != nil {
		if isMongoDuplicate(err) {
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
	if len(input.Name) > 255 {
		jsonError(w, "name too long (max 255)", http.StatusBadRequest)
		return
	}
	if input.ReportTypes == nil {
		input.ReportTypes = []string{}
	}
	input.Barcode = strings.TrimSpace(input.Barcode)

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
		}},
	)
	if err != nil {
		if isMongoDuplicate(err) {
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
// POST /api/drugs/bulk  (ADMIN+)
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
		if inp.Name == "" {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: "-", Message: "ชื่อยาห้ามว่าง"})
			continue
		}
		if inp.SellPrice < 0 {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: inp.Name, Message: "ราคาขายต้องไม่ติดลบ"})
			continue
		}
		if inp.CostPrice < 0 {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: inp.Name, Message: "ราคาทุนต้องไม่ติดลบ"})
			continue
		}
		if inp.Stock < 0 {
			inp.Stock = 0 // clamp negative → 0
		}
		if inp.Type == "" {
			inp.Type = "ยาสามัญ"
		}
		if inp.Unit == "" {
			inp.Unit = "ชิ้น"
		}
		if inp.ReportTypes == nil {
			inp.ReportTypes = []string{}
		}
		inp.Barcode = strings.TrimSpace(inp.Barcode)

		drug := models.Drug{
			Name:        inp.Name,
			GenericName: inp.GenericName,
			Type:        inp.Type,
			Strength:    inp.Strength,
			Barcode:     inp.Barcode,
			SellPrice:   inp.SellPrice,
			CostPrice:   inp.CostPrice,
			Stock:       inp.Stock,
			MinStock:    inp.MinStock,
			RegNo:       inp.RegNo,
			Unit:        inp.Unit,
			ReportTypes: inp.ReportTypes,
			CreatedAt:   time.Now(),
		}
		ctx, cancel := context.WithTimeout(bulkCtx, 5*time.Second)
		_, err := mdb.Drugs().InsertOne(ctx, drug)
		cancel()
		if err != nil {
			msg := "บันทึกไม่สำเร็จ"
			if isMongoDuplicate(err) {
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
// GET /api/drugs/low-stock
func (h *DrugHandler) LowStock(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Match report.go Summary semantics: low-stock = nearly out (stock > 0 and <= threshold).
	// Threshold = min_stock if > 0, else DEFAULT_LOW_STOCK_THRESHOLD (20).
	// Drugs with stock == 0 are surfaced separately as "out of stock".
	cur, err := mdb.Drugs().Find(ctx,
		bson.M{"$expr": bson.M{
			"$and": bson.A{
				bson.M{"$gt": bson.A{"$stock", 0}},
				bson.M{"$lte": bson.A{"$stock", bson.M{"$cond": bson.A{
					bson.M{"$gt": bson.A{"$min_stock", 0}}, "$min_stock", 20,
				}}}},
			},
		}},
		options.Find().SetSort(bson.D{{Key: "stock", Value: 1}}),
	)
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
	jsonOK(w, drugs)
}

// ReorderSuggestions computes per-drug reorder advice from recent sales history.
// Query params:
//   - days       (default 30)   lookback window for averaging sales
//   - lookahead  (default 14)   target cover days (suggest qty = avg_daily * lookahead - stock)
//
// GET /api/drugs/reorder-suggestions
func (h *DrugHandler) ReorderSuggestions(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}
	lookahead := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("lookahead")); err == nil && v > 0 && v <= 180 {
		lookahead = v
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	from := time.Now().AddDate(0, 0, -days)
	totals, err := netTotalsByDrug(ctx, mdb, from, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cur, err := mdb.Drugs().Find(ctx, bson.M{})
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

	const defaultThreshold = 20
	const noSalesDaysLeft = 9999.0 // sentinel for "no sales / infinite cover"

	out := make([]models.ReorderSuggestion, 0, len(drugs))
	for _, d := range drugs {
		qtySold := 0
		if t := totals[d.ID]; t != nil && t.Qty > 0 {
			qtySold = t.Qty
		}
		avgDaily := float64(qtySold) / float64(days)

		threshold := d.MinStock
		if threshold <= 0 {
			threshold = defaultThreshold
		}

		daysLeft := noSalesDaysLeft
		if avgDaily > 0 {
			daysLeft = float64(d.Stock) / avgDaily
		}

		suggested := int(math.Ceil(avgDaily*float64(lookahead))) - d.Stock
		if suggested < 0 {
			suggested = 0
		}

		// Include a drug in the suggestion list when any of:
		//  - out of stock AND had recent sales
		//  - stock at/below threshold (regardless of sales)
		//  - cover days (stock / avg_daily) is less than half the lookahead
		include := false
		switch {
		case d.Stock == 0 && qtySold > 0:
			include = true
		case d.Stock <= threshold:
			include = true
		case daysLeft < float64(lookahead)/2:
			include = true
		}
		if !include {
			continue
		}

		out = append(out, models.ReorderSuggestion{
			DrugID:       d.ID.Hex(),
			DrugName:     d.Name,
			Unit:         d.Unit,
			CurrentStock: d.Stock,
			MinStock:     d.MinStock,
			QtySold:      qtySold,
			AvgDailySale: avgDaily,
			DaysLeft:     daysLeft,
			SuggestedQty: suggested,
			CostPrice:    d.CostPrice,
			SellPrice:    d.SellPrice,
		})
	}

	// Sort: urgency first (stock=0 with sales → smallest days_left → min_stock breaches last)
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].CurrentStock == 0) != (out[j].CurrentStock == 0) {
			return out[i].CurrentStock == 0
		}
		return out[i].DaysLeft < out[j].DaysLeft
	})

	jsonOK(w, out)
}
