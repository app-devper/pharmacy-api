package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/reporting"
	"pharmacy-pos/backend/sales"
)

// Report handlers only translate HTTP: every figure comes from the
// reporting module (ADR-0008).
type ReportHandler struct{ dbm *db.Manager }

func NewReportHandler(d *db.Manager) *ReportHandler { return &ReportHandler{dbm: d} }

// report serves one named report (reporting) for the caller's tenant.
func (h *ReportHandler) report(w http.ResponseWriter, r *http.Request, timeout time.Duration, fn func(ctx context.Context, mdb *db.MongoDB) (any, error)) {
	serve(h.dbm, w, r, timeout, fn)
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
	date := r.URL.Query().Get("date")
	serve(h.dbm, w, r, 10*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		return sales.Day(ctx, mdb, date)
	})
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
	serve(h.dbm, w, r, 15*time.Second, func(ctx context.Context, mdb *db.MongoDB) (any, error) {
		closed, replayed, err := sales.Close(ctx, mdb, strings.TrimSpace(body.Date), strings.TrimSpace(body.ClosedByName))
		markReplayed(w, replayed)
		return closed, err
	})
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
