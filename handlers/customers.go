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

type CustomerHandler struct{ dbm *db.Manager }

func NewCustomerHandler(d *db.Manager) *CustomerHandler { return &CustomerHandler{dbm: d} }

func (h *CustomerHandler) List(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.Customers().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var customers []models.Customer
	if err := cur.All(ctx, &customers); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if customers == nil {
		customers = []models.Customer{}
	}
	jsonOK(w, customers)
}

func (h *CustomerHandler) Add(w http.ResponseWriter, r *http.Request) {
	var input models.CustomerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if input.Disease == "" {
		input.Disease = "-"
	}
	if !isValidPriceTier(input.PriceTier) {
		jsonError(w, "price_tier ต้องเป็น retail|regular|wholesale หรือว่าง", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cust := models.Customer{
		Name:      input.Name,
		Phone:     strings.TrimSpace(input.Phone),
		Disease:   input.Disease,
		PriceTier: input.PriceTier,
		CreatedAt: time.Now(),
	}
	res, err := mdb.Customers().InsertOne(ctx, cust)
	if err != nil {
		if isMongoDuplicate(err) {
			jsonError(w, "เบอร์โทรนี้มีลูกค้าอยู่ในระบบแล้ว", http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cust.ID = res.InsertedID.(bson.ObjectID)
	jsonOK(w, cust)
}

// Update replaces mutable fields of a customer.
func (h *CustomerHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.CustomerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if input.Disease == "" {
		input.Disease = "-"
	}
	if !isValidPriceTier(input.PriceTier) {
		jsonError(w, "price_tier ต้องเป็น retail|regular|wholesale หรือว่าง", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var updated models.Customer
	err = mdb.Customers().FindOneAndUpdate(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"name":       input.Name,
			"phone":      strings.TrimSpace(input.Phone),
			"disease":    input.Disease,
			"price_tier": input.PriceTier,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "customer not found", http.StatusNotFound)
			return
		}
		if isMongoDuplicate(err) {
			jsonError(w, "เบอร์โทรนี้มีลูกค้าอยู่ในระบบแล้ว", http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, updated)
}

// GetSales returns all sales for a specific customer, sorted newest first.
func (h *CustomerHandler) GetSales(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := mdb.Customers().FindOne(ctx, bson.M{"_id": oid}).Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "customer not found", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cur, err := mdb.Sales().Find(ctx,
		bson.M{"customer_id": oid},
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(100),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var sales []models.Sale
	if err := cur.All(ctx, &sales); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sales == nil {
		sales = []models.Sale{}
	}
	jsonOK(w, sales)
}
