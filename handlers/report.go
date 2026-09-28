package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"pharmacy-pos/backend/sales"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/reporting"
)

type ReportHandler struct{ dbm *db.Manager }

func NewReportHandler(d *db.Manager) *ReportHandler { return &ReportHandler{dbm: d} }

func (h *ReportHandler) Summary(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	summary, err := summaryNow(ctx, mdb)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, summary)
}

// summaryNow is today's and this month's net sales, today's bills, and the
// stock summary, by the reporting rules (ADR-0008).
func summaryNow(ctx context.Context, mdb *db.MongoDB) (models.ReportSummary, error) {
	tz := mdb.Timezone(ctx)
	now := time.Now()
	today := reporting.DayStart(now, tz)
	tomorrow := today.AddDate(0, 0, 1)
	todaySales, err := reporting.NetSales(ctx, mdb, today, tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	monthSales, err := reporting.NetSales(ctx, mdb, reporting.MonthStart(now, tz), tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	todayBills, err := reporting.Bills(ctx, mdb, today, tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	value, low, out, err := reporting.Stock(ctx, mdb, loadStockSettings(ctx, mdb).LowStockThreshold)
	if err != nil {
		return models.ReportSummary{}, err
	}
	return models.ReportSummary{
		TodaySales: todaySales, TodayBills: todayBills, MonthSales: monthSales,
		StockValue: value, LowStock: low, OutStock: out,
	}, nil
}

func (h *ReportHandler) Daily(w http.ResponseWriter, r *http.Request) {
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tz := mdb.Timezone(ctx)
	lines, err := reporting.Lines(ctx, mdb, reporting.DayStart(time.Now(), tz).AddDate(0, 0, -days), time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, reporting.Daily(lines, tz))
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

func (h *ReportHandler) Profit(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	from, to := resolveReportRange(r, mdb.Timezone(ctx))

	lines, err := reporting.Lines(ctx, mdb, from, to)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totals := reporting.ByDrug(lines)

	byDrug := make([]models.DrugProfit, 0, len(totals))
	var summary models.ProfitSummary
	for drugID, total := range totals {
		profit := total.Revenue - total.Cost
		margin := 0.0
		if total.Revenue > 0 {
			margin = profit / total.Revenue * 100
		}
		byDrug = append(byDrug, models.DrugProfit{
			DrugID:   drugID.Hex(),
			DrugName: total.DrugName,
			QtySold:  total.Qty,
			Revenue:  total.Revenue,
			Cost:     total.Cost,
			Profit:   profit,
			Margin:   margin,
		})
		summary.Revenue += total.Revenue
		summary.Cost += total.Cost
		summary.Profit += profit
	}
	sort.Slice(byDrug, func(i, j int) bool { return byDrug[i].Profit > byDrug[j].Profit })

	if summary.Revenue > 0 {
		summary.Margin = summary.Profit / summary.Revenue * 100
	}
	if summary.Bills, err = reporting.Bills(ctx, mdb, from, to); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, models.ProfitReport{Summary: summary, ByDrug: byDrug})
}

func (h *ReportHandler) TopDrugs(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	lines, err := reporting.Lines(ctx, mdb, reporting.DayStart(time.Now(), mdb.Timezone(ctx)).AddDate(0, 0, -days), time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]models.TopDrug, 0)
	for drugID, total := range reporting.ByDrug(lines) {
		if total.Qty <= 0 && total.Revenue <= 0 {
			continue
		}
		result = append(result, models.TopDrug{
			DrugID:   drugID.Hex(),
			DrugName: total.DrugName,
			QtySold:  total.Qty,
			Revenue:  total.Revenue,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QtySold > result[j].QtySold })
	if len(result) > 10 {
		result = result[:10]
	}
	jsonOK(w, result)
}

func (h *ReportHandler) SlowDrugs(w http.ResponseWriter, r *http.Request) {
	days := 90
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	since := time.Now().AddDate(0, 0, -days)
	pipeline := bson.A{
		bson.M{"$match": notVoided(bson.M{"sold_at": bson.M{"$gte": since}})},
		bson.M{"$lookup": bson.M{
			"from":         "sale_items",
			"localField":   "_id",
			"foreignField": "sale_id",
			"as":           "items",
		}},
		bson.M{"$unwind": "$items"},
		bson.M{"$group": bson.M{"_id": "$items.drug_id"}},
	}
	cur, err := mdb.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)
	var soldRaw []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &soldRaw); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	soldIDs := make([]bson.ObjectID, len(soldRaw))
	for i, s := range soldRaw {
		soldIDs[i] = s.ID
	}

	filter := bson.M{"stock": bson.M{"$gt": 0}, "_id": bson.M{"$nin": soldIDs}}
	drugCur, err := mdb.Drugs().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "stock", Value: -1}}).SetLimit(30))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer drugCur.Close(ctx)

	var result []models.SlowDrug
	if err := drugCur.All(ctx, &result); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		result = []models.SlowDrug{}
	}
	jsonOK(w, result)
}

// monthsBack is the start of the month (months-1) months before now, so the
// report covers `months` calendar months including the current one.
func monthsBack(tz *time.Location, months int) time.Time {
	return reporting.MonthStart(time.Now(), tz).AddDate(0, -(months - 1), 0)
}

func (h *ReportHandler) Monthly(w http.ResponseWriter, r *http.Request) {
	months := 12
	if m, err := strconv.Atoi(r.URL.Query().Get("months")); err == nil && m > 0 {
		months = m
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tz := mdb.Timezone(ctx)
	lines, err := reporting.Lines(ctx, mdb, monthsBack(tz, months), time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, reporting.Monthly(lines, tz))
}

// Dashboard bundles summary + daily + monthly + recent_sales into a single response
// so ReportPage only makes one HTTP call on initial load.
// GET /api/pharmacy/v1/report/dashboard?days=7
func (h *ReportHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 365 {
		days = d
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tz := mdb.Timezone(ctx)

	summary, err := summaryNow(ctx, mdb)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Load once from the earlier of the two chart windows.
	since := monthsBack(tz, 12)
	if d := reporting.DayStart(time.Now(), tz).AddDate(0, 0, -days); d.Before(since) {
		since = d
	}
	lines, err := reporting.Lines(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dailyFrom := reporting.DayStart(time.Now(), tz).AddDate(0, 0, -days)
	monthlyFrom := monthsBack(tz, 12)
	var dailyLines, monthlyLines []reporting.Line
	for _, l := range lines {
		if !l.At.Before(dailyFrom) {
			dailyLines = append(dailyLines, l)
		}
		if !l.At.Before(monthlyFrom) {
			monthlyLines = append(monthlyLines, l)
		}
	}

	var recent []models.Sale
	cur, err := mdb.Sales().Find(ctx, bson.M{"voided": bson.M{"$ne": true}},
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(5),
	)
	if err == nil {
		err = cur.All(ctx, &recent)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recent == nil {
		recent = []models.Sale{}
	}

	jsonOK(w, models.Dashboard{
		Summary:     summary,
		Daily:       reporting.Daily(dailyLines, tz),
		Monthly:     reporting.Monthly(monthlyLines, tz),
		RecentSales: recent,
	})
}

func notVoided(filter bson.M) bson.M {
	merged := bson.M{"voided": bson.M{"$ne": true}}
	for k, v := range filter {
		merged[k] = v
	}
	return merged
}

// resolveReportRange is [from, to) from ?from=&to= (dates, to inclusive).
func resolveReportRange(r *http.Request, tz *time.Location) (time.Time, time.Time) {
	now := time.Now().In(tz)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz).AddDate(0, 0, 1)

	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
			to = t.AddDate(0, 0, 1)
		}
	}
	return from, to
}
