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

type CustomerHandler struct{ db *db.MongoDB }

func NewCustomerHandler(d *db.MongoDB) *CustomerHandler { return &CustomerHandler{db: d} }

func (h *CustomerHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.Customers().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cust := models.Customer{
		Name:      input.Name,
		Phone:     input.Phone,
		Disease:   input.Disease,
		CreatedAt: time.Now(),
	}
	res, err := h.db.Customers().InsertOne(ctx, cust)
	if err != nil {
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var updated models.Customer
	err = h.db.Customers().FindOneAndUpdate(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"name":    input.Name,
			"phone":   input.Phone,
			"disease": input.Disease,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.Sales().Find(ctx,
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
