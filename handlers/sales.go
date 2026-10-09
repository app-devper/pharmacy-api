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

	"pharmacy-pos/backend/calendar"
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

	// Date range filter: inclusive dates in the pharmacy's calendar.
	if from, to := calendar.Dates(tz, q.Get("from"), q.Get("to")); !from.IsZero() || !to.IsZero() {
		dateFilter := bson.M{}
		if !from.IsZero() {
			dateFilter["$gte"] = from
		}
		if !to.IsZero() {
			dateFilter["$lt"] = to
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

// Items lists a sale's lines with what each can still return (sales.Lines).
func (h *SaleHandler) Items(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	lines, err := sales.Lines(ctx, mdb, chi.URLParam(r, "id"))
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, lines)
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

// Abandon closes a queued sale, or a recorded sale's refused KY forms, that
// will not be recorded (ADR-0009). A queued sale that was recorded meanwhile
// is a 409 carrying the sale, so the client marks its entry synced.
func (h *SaleHandler) Abandon(w http.ResponseWriter, r *http.Request) {
	var input models.AbandonInput
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

	out, replayed, err := sales.Abandon(ctx, mdb, input)
	var sold *sales.AlreadySold
	if errors.As(err, &sold) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": sold.Error(), "sale": sold.Sale})
		return
	}
	if err != nil {
		writeCommandError(w, err)
		return
	}
	markReplayed(w, replayed)
	jsonOK(w, out)
}

// Abandoned lists abandonments, newest first (limit, default 200).
func (h *SaleHandler) Abandoned(w http.ResponseWriter, r *http.Request) {
	limit := int64(200)
	if l, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && l > 0 {
		limit = l
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out, err := sales.Abandoned(ctx, mdb, limit)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, out)
}
