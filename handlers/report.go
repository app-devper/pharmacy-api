package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/reporting"
	"pharmacy-pos/backend/sales"
)

// Report handlers only translate HTTP: every figure comes from the
// reporting module (ADR-0008).
type ReportHandler struct{ dbm *db.Manager }

func NewReportHandler(d *db.Manager) *ReportHandler { return &ReportHandler{dbm: d} }

// intParam is ?key= as a positive int, def when absent, malformed, not
// positive, or above max (max 0 means no upper bound).
func intParam(r *http.Request, key string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v <= 0 || (max > 0 && v > max) {
		return def
	}
	return v
}

// report runs fn against the caller's tenant and writes its answer.
func (h *ReportHandler) report(w http.ResponseWriter, r *http.Request, timeout time.Duration, fn func(ctx context.Context, mdb *db.MongoDB) (any, error)) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	out, err := fn(ctx, mdb)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, out)
}

func (h *ReportHandler) Summary(w http.ResponseWriter, r *http.Request) {
	h.report(w, r, 10*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.Summary(ctx, mdb, time.Now(), loadStockSettings(ctx, mdb).LowStockThreshold)
	})
}

// Daily — GET /report/daily?days=7
func (h *ReportHandler) Daily(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 7, 0)
	h.report(w, r, 10*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.Daily(ctx, mdb, time.Now(), days)
	})
}

// Eod is the End-of-day view of ?date= (default today): live for an open
// day, the close snapshot plus Late sale adjustments for a closed one.
func (h *ReportHandler) Eod(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	day, err := sales.Day(ctx, mdb, r.URL.Query().Get("date"))
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, day)
}

// CloseEod records the End-of-day close of a business day (ADR-0006).
func (h *ReportHandler) CloseEod(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date         string `json:"date"`
		ClosedByName string `json:"closed_by_name"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	closed, replayed, err := sales.Close(ctx, mdb, strings.TrimSpace(body.Date), strings.TrimSpace(body.ClosedByName))
	if err != nil {
		writeCommandError(w, err)
		return
	}
	markReplayed(w, replayed)
	jsonOK(w, closed)
}

// Profit — GET /report/profit?from=YYYY-MM-DD&to=YYYY-MM-DD (to inclusive;
// default this month to today).
func (h *ReportHandler) Profit(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	h.report(w, r, 15*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.Profit(ctx, mdb, time.Now(), from, to)
	})
}

// TopDrugs — GET /report/top-drugs?days=30
func (h *ReportHandler) TopDrugs(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 30, 0)
	h.report(w, r, 10*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.TopDrugs(ctx, mdb, time.Now(), days)
	})
}

// SlowDrugs — GET /report/slow-drugs?days=90
func (h *ReportHandler) SlowDrugs(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 90, 0)
	h.report(w, r, 10*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.SlowDrugs(ctx, mdb, time.Now(), days)
	})
}

// Monthly — GET /report/monthly?months=12
func (h *ReportHandler) Monthly(w http.ResponseWriter, r *http.Request) {
	months := intParam(r, "months", 12, 0)
	h.report(w, r, 15*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.Monthly(ctx, mdb, time.Now(), months)
	})
}

// Dashboard bundles summary + daily + monthly + recent_sales into a single response
// so ReportPage only makes one HTTP call on initial load.
// GET /api/pharmacy/v1/report/dashboard?days=7
func (h *ReportHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 7, 365)
	h.report(w, r, 20*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return reporting.Dashboard(ctx, mdb, time.Now(), days, loadStockSettings(ctx, mdb).LowStockThreshold)
	})
}
