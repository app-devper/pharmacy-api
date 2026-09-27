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
	"pharmacy-pos/backend/sales"
)

type ReturnHandler struct{ dbm *db.Manager }

func NewReturnHandler(d *db.Manager) *ReturnHandler { return &ReturnHandler{dbm: d} }

// Create processes a partial drug return linked to an existing sale.
// POST /api/pharmacy/v1/sales/{id}/return
// Create records a return through the sales module (pharmacy ADR-0002, ADR-0005).
func (h *ReturnHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.DrugReturnInput
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

	out, replayed, err := sales.Return(ctx, mdb, chi.URLParam(r, "id"), input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	markReplayed(w, replayed)
	jsonOK(w, out)
}

// List returns all drug returns for a sale.
// GET /api/pharmacy/v1/sales/{id}/returns
func (h *ReturnHandler) List(w http.ResponseWriter, r *http.Request) {
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

	cur, err := mdb.DrugReturns().Find(ctx,
		bson.M{"sale_id": oid},
		options.Find().SetSort(bson.D{{Key: "returned_at", Value: -1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var returns []models.DrugReturn
	if err := cur.All(ctx, &returns); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if returns == nil {
		returns = []models.DrugReturn{}
	}
	jsonOK(w, returns)
}
