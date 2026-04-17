package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func (h *DrugHandler) List(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.Drugs().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
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
	if input.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if input.Type == "" {
		input.Type = "ยาสามัญ"
	}
	if input.Unit == "" {
		input.Unit = "ชิ้น"
	}
	input.Barcode = strings.TrimSpace(input.Barcode)

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if input.ReportTypes == nil {
		input.ReportTypes = []string{}
	}
	drug := models.Drug{
		Name:        input.Name,
		GenericName: input.GenericName,
		Type:        input.Type,
		Strength:    input.Strength,
		Barcode:     input.Barcode,
		SellPrice:   input.SellPrice,
		CostPrice:   input.CostPrice,
		Stock:       input.Stock,
		MinStock:    input.MinStock,
		RegNo:       input.RegNo,
		Unit:        input.Unit,
		ReportTypes: input.ReportTypes,
		CreatedAt:   time.Now(),
	}
	res, err := mdb.Drugs().InsertOne(ctx, drug)
	if err != nil {
		if isMongoDuplicate(err) {
			jsonError(w, "บาร์โค้ดนี้มีอยู่ในระบบแล้ว", http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	drug.ID = res.InsertedID.(bson.ObjectID)
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

	_, err = mdb.Drugs().UpdateOne(ctx,
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

	result := BulkImportResult{Errors: []BulkImportRowError{}}
	for i, inp := range input.Drugs {
		row := i + 2 // row 1 = header in the Excel sheet
		if inp.Name == "" {
			result.Errors = append(result.Errors, BulkImportRowError{Row: row, Name: "-", Message: "ชื่อยาห้ามว่าง"})
			continue
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
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
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

	cur, err := mdb.Drugs().Find(ctx,
		bson.M{"$expr": bson.M{
			"$and": bson.A{
				bson.M{"$gt": bson.A{"$min_stock", 0}},
				bson.M{"$lte": bson.A{"$stock", "$min_stock"}},
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
