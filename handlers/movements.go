package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pharmacy-pos/backend/calendar"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	mw "pharmacy-pos/backend/middleware"
)

type MovementsHandler struct{ dbm *db.Manager }

func NewMovementsHandler(d *db.Manager) *MovementsHandler { return &MovementsHandler{dbm: d} }

// List handles GET /api/pharmacy/v1/movements through the inventory module
// (ADR-0010). Query params: from, to (YYYY-MM-DD), drug_name, types
// (comma-sep), limit, offset.
func (h *MovementsHandler) List(w http.ResponseWriter, r *http.Request) {
	d, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	q := r.URL.Query()
	tz := d.Timezone(ctx)

	// Inclusive dates in the pharmacy's calendar; by default the last 30
	// days and today, whole days.
	today := calendar.DayStart(time.Now(), tz)
	query := inventory.MovementQuery{
		From:     today.AddDate(0, 0, -30),
		To:       today.AddDate(0, 0, 1),
		Kinds:    inventory.AllMoves,
		DrugName: strings.TrimSpace(q.Get("drug_name")),
	}
	from, to := calendar.Dates(tz, q.Get("from"), q.Get("to"))
	if !from.IsZero() {
		query.From = from
	}
	if !to.IsZero() {
		query.To = to
	}
	if tp := q.Get("types"); tp != "" {
		query.Kinds = nil
		for _, t := range strings.Split(tp, ",") {
			query.Kinds = append(query.Kinds, strings.TrimSpace(t))
		}
	}

	limit := 50
	offset := 0
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	all, err := inventory.Movements(ctx, d, query)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total := len(all)
	page := []inventory.Movement{}
	if offset < total {
		page = all[offset:min(offset+limit, total)]
	}
	jsonOK(w, map[string]interface{}{"total": total, "items": page})
}
