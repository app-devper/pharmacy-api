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

type SupplierHandler struct{ db *db.MongoDB }

func NewSupplierHandler(d *db.MongoDB) *SupplierHandler { return &SupplierHandler{db: d} }

// List returns all suppliers sorted by name, with optional ?q= name filter.
func (h *SupplierHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	filter := bson.M{}
	if q := r.URL.Query().Get("q"); q != "" {
		filter["name"] = bson.M{"$regex": q, "$options": "i"}
	}

	cur, err := h.db.Suppliers().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var list []models.Supplier
	if err := cur.All(ctx, &list); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []models.Supplier{}
	}
	jsonOK(w, list)
}

// Create inserts a new supplier.
func (h *SupplierHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.SupplierInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	supplier := models.Supplier{
		Name:        input.Name,
		ContactName: input.ContactName,
		Phone:       input.Phone,
		Address:     input.Address,
		TaxID:       input.TaxID,
		Notes:       input.Notes,
		CreatedAt:   time.Now(),
	}
	res, err := h.db.Suppliers().InsertOne(ctx, supplier)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	supplier.ID = res.InsertedID.(bson.ObjectID)
	jsonOK(w, supplier)
}

// Update replaces mutable fields of a supplier.
func (h *SupplierHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.SupplierInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var updated models.Supplier
	err = h.db.Suppliers().FindOneAndUpdate(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"name":         input.Name,
			"contact_name": input.ContactName,
			"phone":        input.Phone,
			"address":      input.Address,
			"tax_id":       input.TaxID,
			"notes":        input.Notes,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, updated)
}

// Delete removes a supplier by ID.
func (h *SupplierHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err = h.db.Suppliers().DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}
