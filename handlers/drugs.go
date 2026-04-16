package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

type DrugHandler struct{ db *db.MongoDB }

func NewDrugHandler(d *db.MongoDB) *DrugHandler { return &DrugHandler{db: d} }

func (h *DrugHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.Drugs().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
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
		input.Unit = "เม็ด"
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
	res, err := h.db.Drugs().InsertOne(ctx, drug)
	if err != nil {
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err = h.db.Drugs().UpdateOne(ctx,
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
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// LowStock returns drugs where min_stock > 0 AND stock <= min_stock, sorted by stock ASC.
// GET /api/drugs/low-stock
func (h *DrugHandler) LowStock(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.Drugs().Find(ctx,
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
