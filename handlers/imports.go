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
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/receiving"
)

type ImportHandler struct{ dbm *db.Manager }

func NewImportHandler(d *db.Manager) *ImportHandler { return &ImportHandler{dbm: d} }

// List returns all purchase orders (summary only, items excluded).
func (h *ImportHandler) List(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	filter := bson.M{}
	if s := r.URL.Query().Get("supplier"); s != "" {
		filter["supplier"] = s
	}

	cur, err := mdb.PurchaseOrders().Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(200).
			SetProjection(bson.M{"items": 0}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var list []models.PurchaseOrderSummary
	if err := cur.All(ctx, &list); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []models.PurchaseOrderSummary{}
	}
	jsonOK(w, list)
}

// GetOne returns the full purchase order including items.
func (h *ImportHandler) GetOne(w http.ResponseWriter, r *http.Request) {
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

	var po models.PurchaseOrder
	if err := mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	jsonOK(w, po)
}

// Create drafts a purchase order through the receiving module (ADR-0012).
func (h *ImportHandler) Create(w http.ResponseWriter, r *http.Request) {
	h.withInput(w, r, func(ctx context.Context, mdb *db.MongoDB, in models.POInput) (models.PurchaseOrder, error) {
		return receiving.Draft(ctx, mdb, in)
	})
}

// Update revises a draft purchase order.
func (h *ImportHandler) Update(w http.ResponseWriter, r *http.Request) {
	h.withInput(w, r, func(ctx context.Context, mdb *db.MongoDB, in models.POInput) (models.PurchaseOrder, error) {
		return receiving.Revise(ctx, mdb, chi.URLParam(r, "id"), in)
	})
}

// Confirm receives a draft's lines as lots with their ขย.9 rows.
func (h *ImportHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	po, err := receiving.Confirm(ctx, mdb, chi.URLParam(r, "id"))
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, po)
}

// Delete discards a draft purchase order.
func (h *ImportHandler) Delete(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := receiving.Discard(ctx, mdb, chi.URLParam(r, "id")); err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func (h *ImportHandler) withInput(w http.ResponseWriter, r *http.Request,
	run func(context.Context, *db.MongoDB, models.POInput) (models.PurchaseOrder, error)) {
	var input models.POInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	po, err := run(ctx, mdb, input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, po)
}
