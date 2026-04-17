package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
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

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	todaySales := sumSales(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})
	todayBills := countDocs(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})
	monthSales := sumSales(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": startOfMonth}})
	stockValue := calcStockValue(ctx, mdb)
	lowStock := int(countDrugs(ctx, mdb, bson.M{"stock": bson.M{"$gt": 0, "$lte": 20}}))
	outStock := int(countDrugs(ctx, mdb, bson.M{"stock": 0}))

	jsonOK(w, models.ReportSummary{
		TodaySales: todaySales,
		TodayBills: int(todayBills),
		MonthSales: monthSales,
		StockValue: stockValue,
		LowStock:   lowStock,
		OutStock:   outStock,
	})
}

func (h *ReportHandler) Daily(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days := 7
	if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
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
		bson.M{"$group": bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$sold_at"}},
			"total": bson.M{"$sum": "$total"},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
		bson.M{"$project": bson.M{"day": "$_id", "total": 1, "_id": 0}},
	}

	cur, err := mdb.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var result []models.DailyData
	if err := cur.All(ctx, &result); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		result = []models.DailyData{}
	}
	jsonOK(w, result)
}

// Eod — End-of-Day summary for cash reconciliation.
// GET /api/report/eod?date=YYYY-MM-DD  (default: today)
func (h *ReportHandler) Eod(w http.ResponseWriter, r *http.Request) {
	dateStr := r.URL.Query().Get("date")

	var startOfDay, endOfDay time.Time
	if dateStr != "" {
		t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			jsonError(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		startOfDay = t
	} else {
		now := time.Now()
		startOfDay = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		dateStr = startOfDay.Format("2006-01-02")
	}
	endOfDay = startOfDay.Add(24 * time.Hour)

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	filter := notVoided(bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})

	// Fetch all bills for the day
	cur, err := mdb.Sales().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: 1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var bills []models.Sale
	if err := cur.All(ctx, &bills); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if bills == nil {
		bills = []models.Sale{}
	}

	// Compute totals
	var totalSales, totalDisc, totalRec, totalChange float64
	for _, b := range bills {
		totalSales += b.Total
		totalDisc += b.Discount
		totalRec += b.Received
		totalChange += b.Change
	}

	jsonOK(w, models.EodReport{
		Date:          dateStr,
		BillCount:     len(bills),
		TotalSales:    totalSales,
		TotalDiscount: totalDisc,
		TotalReceived: totalRec,
		TotalChange:   totalChange,
		NetCash:       totalRec - totalChange,
		Bills:         bills,
	})
}

// Profit — gross profit breakdown by drug for a date range.
// GET /api/report/profit?from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *ReportHandler) Profit(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location())

	from := startOfMonth
	to := endOfDay

	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			to = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
		}
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dateFilter := bson.M{"sold_at": bson.M{"$gte": from, "$lte": to}}

	pipeline := bson.A{
		// 1. Match non-voided sales in date range
		bson.M{"$match": notVoided(dateFilter)},
		// 2. Lookup sale items
		bson.M{"$lookup": bson.M{
			"from":         "sale_items",
			"localField":   "_id",
			"foreignField": "sale_id",
			"as":           "items",
		}},
		// 3. Unwind items
		bson.M{"$unwind": "$items"},
		// 4. Lookup drug for current cost_price
		bson.M{"$lookup": bson.M{
			"from":         "drugs",
			"localField":   "items.drug_id",
			"foreignField": "_id",
			"as":           "drug_info",
		}},
		// 5. Unwind drug_info (preserve for deleted drugs — cost will be 0)
		bson.M{"$unwind": bson.M{
			"path":                       "$drug_info",
			"preserveNullAndEmptyArrays": true,
		}},
		// 6. Group by drug
		bson.M{"$group": bson.M{
			"_id":      "$items.drug_id",
			"drug_name": bson.M{"$first": "$items.drug_name"},
			"qty_sold": bson.M{"$sum": "$items.qty"},
			"revenue":  bson.M{"$sum": "$items.subtotal"},
			"cost": bson.M{"$sum": bson.M{
				"$multiply": bson.A{
					"$items.qty",
					bson.M{"$ifNull": bson.A{"$drug_info.cost_price", 0}},
				},
			}},
		}},
		// 7. Add profit and margin
		bson.M{"$addFields": bson.M{
			"profit": bson.M{"$subtract": bson.A{"$revenue", "$cost"}},
			"margin": bson.M{"$cond": bson.A{
				bson.M{"$gt": bson.A{"$revenue", 0}},
				bson.M{"$multiply": bson.A{
					bson.M{"$divide": bson.A{
						bson.M{"$subtract": bson.A{"$revenue", "$cost"}},
						"$revenue",
					}},
					100,
				}},
				0,
			}},
		}},
		// 8. Sort by profit descending
		bson.M{"$sort": bson.M{"profit": -1}},
	}

	cur, err := mdb.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var byDrug []models.DrugProfit
	if err := cur.All(ctx, &byDrug); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if byDrug == nil {
		byDrug = []models.DrugProfit{}
	}

	// Build summary from byDrug results (no extra DB round-trip)
	var summary models.ProfitSummary
	for _, d := range byDrug {
		summary.Revenue += d.Revenue
		summary.Cost += d.Cost
		summary.Profit += d.Profit
	}
	if summary.Revenue > 0 {
		summary.Margin = summary.Profit / summary.Revenue * 100
	}
	summary.Bills = int(countDocs(ctx, mdb, dateFilter))

	jsonOK(w, models.ProfitReport{Summary: summary, ByDrug: byDrug})
}

// TopDrugs returns the top 10 best-selling drugs by quantity for the last N days.
// GET /api/report/top-drugs?days=N  (default 30)
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
		bson.M{"$group": bson.M{
			"_id":      "$items.drug_id",
			"drug_name": bson.M{"$first": "$items.drug_name"},
			"qty_sold": bson.M{"$sum": "$items.qty"},
			"revenue":  bson.M{"$sum": "$items.subtotal"},
		}},
		bson.M{"$sort": bson.M{"qty_sold": -1}},
		bson.M{"$limit": 10},
	}

	cur, err := mdb.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var result []models.TopDrug
	if err := cur.All(ctx, &result); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		result = []models.TopDrug{}
	}
	jsonOK(w, result)
}

// SlowDrugs returns drugs that have stock > 0 but no sales in the last N days.
// GET /api/report/slow-drugs?days=N  (default 90)
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

	// Collect drug_ids that had sales in the window
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
	var soldRaw []struct{ ID bson.ObjectID `bson:"_id"` }
	if err := cur.All(ctx, &soldRaw); err != nil {
		cur.Close(ctx)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cur.Close(ctx)

	soldIDs := make([]bson.ObjectID, len(soldRaw))
	for i, s := range soldRaw {
		soldIDs[i] = s.ID
	}

	// Find drugs with stock > 0 not in soldIDs
	filter := bson.M{
		"stock": bson.M{"$gt": 0},
		"_id":   bson.M{"$nin": soldIDs},
	}
	drugCur, err := mdb.Drugs().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "stock", Value: -1}}).SetLimit(30),
	)
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

// Monthly returns monthly revenue vs cost for the last N months.
// GET /api/report/monthly?months=N  (default 12)
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

	since := time.Now().AddDate(0, -months, 0)

	pipeline := bson.A{
		bson.M{"$match": notVoided(bson.M{"sold_at": bson.M{"$gte": since}})},
		bson.M{"$lookup": bson.M{
			"from":         "sale_items",
			"localField":   "_id",
			"foreignField": "sale_id",
			"as":           "items",
		}},
		bson.M{"$unwind": "$items"},
		bson.M{"$lookup": bson.M{
			"from":         "drugs",
			"localField":   "items.drug_id",
			"foreignField": "_id",
			"as":           "drug_info",
		}},
		bson.M{"$unwind": bson.M{
			"path":                       "$drug_info",
			"preserveNullAndEmptyArrays": true,
		}},
		bson.M{"$group": bson.M{
			"_id": bson.M{"$dateToString": bson.M{"format": "%Y-%m", "date": "$sold_at"}},
			"revenue": bson.M{"$sum": "$items.subtotal"},
			"cost": bson.M{"$sum": bson.M{
				"$multiply": bson.A{
					"$items.qty",
					bson.M{"$ifNull": bson.A{"$drug_info.cost_price", 0}},
				},
			}},
		}},
		bson.M{"$addFields": bson.M{
			"profit": bson.M{"$subtract": bson.A{"$revenue", "$cost"}},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
		bson.M{"$project": bson.M{
			"month":   "$_id",
			"revenue": 1,
			"cost":    1,
			"profit":  1,
			"_id":     0,
		}},
	}

	cur, err := mdb.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var result []models.MonthlyData
	if err := cur.All(ctx, &result); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		result = []models.MonthlyData{}
	}
	jsonOK(w, result)
}

// Helpers

// notVoided merges the "exclude voided" condition into a copy of filter.
func notVoided(filter bson.M) bson.M {
	merged := bson.M{"voided": bson.M{"$ne": true}}
	for k, v := range filter {
		merged[k] = v
	}
	return merged
}

func sumSales(ctx context.Context, d *db.MongoDB, filter bson.M) float64 {
	pipeline := bson.A{
		bson.M{"$match": notVoided(filter)},
		bson.M{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": "$total"}}},
	}
	cur, err := d.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		return 0
	}
	defer cur.Close(ctx)
	var res []struct{ Total float64 `bson:"total"` }
	cur.All(ctx, &res)
	if len(res) == 0 {
		return 0
	}
	return res[0].Total
}

func countDocs(ctx context.Context, d *db.MongoDB, filter bson.M) int64 {
	n, _ := d.Sales().CountDocuments(ctx, notVoided(filter))
	return n
}

func countDrugs(ctx context.Context, d *db.MongoDB, filter bson.M) int64 {
	n, _ := d.Drugs().CountDocuments(ctx, filter)
	return n
}

func calcStockValue(ctx context.Context, d *db.MongoDB) float64 {
	pipeline := bson.A{
		bson.M{"$group": bson.M{
			"_id":   nil,
			"total": bson.M{"$sum": bson.M{"$multiply": bson.A{"$price", "$stock"}}},
		}},
	}
	cur, err := d.Drugs().Aggregate(ctx, pipeline)
	if err != nil {
		return 0
	}
	defer cur.Close(ctx)
	var res []struct{ Total float64 `bson:"total"` }
	cur.All(ctx, &res)
	if len(res) == 0 {
		return 0
	}
	return res[0].Total
}
