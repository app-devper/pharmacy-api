package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/sales"
)

type SaleHandler struct{ dbm *db.Manager }

func NewSaleHandler(d *db.Manager) *SaleHandler { return &SaleHandler{dbm: d} }

func (h *SaleHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limitStr := q.Get("limit")
	limit := int64(200)
	if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 {
		limit = l
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tz := mdb.Timezone(ctx)

	filter := bson.M{}

	// Date range filter
	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr != "" || toStr != "" {
		dateFilter := bson.M{}
		if fromStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", fromStr, tz); err == nil {
				dateFilter["$gte"] = t
			}
		}
		if toStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", toStr, tz); err == nil {
				dateFilter["$lt"] = t.Add(24 * time.Hour)
			}
		}
		filter["sold_at"] = dateFilter
	}

	// Search by bill_no or customer_name
	if search := q.Get("q"); search != "" {
		escapedSearch := regexp.QuoteMeta(search)
		filter["$or"] = bson.A{
			bson.M{"bill_no": bson.M{"$regex": escapedSearch, "$options": "i"}},
			bson.M{"customer_name": bson.M{"$regex": escapedSearch, "$options": "i"}},
		}
	}

	cur, err := mdb.Sales().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(limit),
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

// Create confirms a sale through the sales module (pharmacy ADR-0005).
func (h *SaleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.SaleInput
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

	out, replayed, err := sales.Sell(ctx, mdb, input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	markReplayed(w, replayed)
	jsonOK(w, out)
}

func (h *SaleHandler) Items(w http.ResponseWriter, r *http.Request) {
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

	cur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []models.SaleItem{}
	}
	jsonOK(w, items)
}

// Void cancels a sale: marks it voided, restores drug stock, reverses customer spend.
// Void cancels a whole sale through the sales module.
func (h *SaleHandler) Void(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
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

	if err := sales.Void(ctx, mdb, chi.URLParam(r, "id"), body.Reason); err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// writeCommandError renders a sales or inventory refusal with its status;
// anything else is an unexpected 500.
func writeCommandError(w http.ResponseWriter, err error) {
	var refusal *sales.Error
	if !errors.As(err, &refusal) {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := map[sales.Kind]int{
		sales.Invalid:  http.StatusBadRequest,
		sales.NotFound: http.StatusNotFound,
		sales.Conflict: http.StatusConflict,
	}[refusal.Kind]
	jsonError(w, refusal.Msg, status)
}

// markReplayed tells the caller (and logs) that a Commercial command's
// recorded outcome was returned instead of running it again.
func markReplayed(w http.ResponseWriter, replayed bool) {
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
}
