package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type ReportHandler struct{ dbm *db.Manager }

func NewReportHandler(d *db.Manager) *ReportHandler { return &ReportHandler{dbm: d} }

type saleItemReportRow struct {
	DrugID       bson.ObjectID `bson:"drug_id"`
	DrugName     string        `bson:"drug_name"`
	Qty          int           `bson:"qty"`
	Subtotal     float64       `bson:"subtotal"`
	CostSubtotal float64       `bson:"cost_subtotal"`
	At           time.Time     `bson:"at"`
}

type returnItemReportRow struct {
	DrugID       bson.ObjectID `bson:"drug_id"`
	DrugName     string        `bson:"drug_name"`
	Qty          int           `bson:"qty"`
	Subtotal     float64       `bson:"subtotal"`
	CostSubtotal float64       `bson:"cost_subtotal"`
	At           time.Time     `bson:"at"`
}

type netDrugTotals struct {
	DrugName string
	Qty      int
	Revenue  float64
	Cost     float64
}

func (h *ReportHandler) Summary(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tz := loadTimezone(ctx, mdb)
	now := time.Now().In(tz)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
	endOfDay := startOfDay.Add(24 * time.Hour)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)

	todaySales, err := netSalesAmount(ctx, mdb, startOfDay, endOfDay)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	monthSales, err := netSalesAmount(ctx, mdb, startOfMonth, endOfDay)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	todayBills := countDocs(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})
	stockValue, err := calcStockValue(ctx, mdb)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// low-stock = 1 <= stock <= threshold,
	// where threshold = min_stock (when > 0) else Settings.stock.low_stock_threshold.
	lowThreshold := loadStockSettings(ctx, mdb).LowStockThreshold
	lowStock := int(countDrugs(ctx, mdb, bson.M{
		"$expr": bson.M{"$and": bson.A{
			bson.M{"$gt": bson.A{"$stock", 0}},
			bson.M{"$lte": bson.A{"$stock", bson.M{"$cond": bson.A{
				bson.M{"$gt": bson.A{"$min_stock", 0}}, "$min_stock", lowThreshold,
			}}}},
		}},
	}))
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

	sinceRaw := time.Now().AddDate(0, 0, -days)
	since := time.Date(sinceRaw.Year(), sinceRaw.Month(), sinceRaw.Day(), 0, 0, 0, 0, sinceRaw.Location())
	saleItems, err := loadSaleItemRows(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	returnItems, err := loadReturnItemRows(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dayTotals := map[string]float64{}
	for _, item := range saleItems {
		dayTotals[item.At.Format("2006-01-02")] += item.Subtotal
	}
	for _, item := range returnItems {
		dayTotals[item.At.Format("2006-01-02")] -= item.Subtotal
	}

	daysList := make([]string, 0, len(dayTotals))
	for day := range dayTotals {
		daysList = append(daysList, day)
	}
	sort.Strings(daysList)

	result := make([]models.DailyData, 0, len(daysList))
	for _, day := range daysList {
		result = append(result, models.DailyData{Day: day, Total: dayTotals[day]})
	}
	jsonOK(w, result)
}

func (h *ReportHandler) Eod(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tz := loadTimezone(ctx, mdb)

	dateStr := r.URL.Query().Get("date")
	var startOfDay time.Time
	if dateStr != "" {
		t, err := time.ParseInLocation("2006-01-02", dateStr, tz)
		if err != nil {
			jsonError(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		startOfDay = t
	} else {
		now := time.Now().In(tz)
		startOfDay = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
		dateStr = startOfDay.Format("2006-01-02")
	}
	endOfDay := startOfDay.Add(24 * time.Hour)

	filter := notVoided(bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})
	cur, err := mdb.Sales().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "sold_at", Value: 1}}))
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

	refunds, err := sumReturnRefunds(ctx, mdb, startOfDay, endOfDay)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

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
		TotalSales:    totalSales - refunds,
		TotalDiscount: totalDisc,
		TotalReceived: totalRec,
		TotalChange:   totalChange,
		NetCash:       totalRec - totalChange - refunds,
		Bills:         bills,
	})
}

func (h *ReportHandler) Profit(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	from, to := resolveReportRange(r, loadTimezone(ctx, mdb))

	totals, err := netTotalsByDrug(ctx, mdb, from, to)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

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
	summary.Bills = int(countDocs(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": from, "$lte": to}}))

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

	since := time.Now().AddDate(0, 0, -days)
	totals, err := netTotalsByDrug(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]models.TopDrug, 0, len(totals))
	for drugID, total := range totals {
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
	var soldRaw []struct {
		ID bson.ObjectID `bson:"_id"`
	}
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
	saleItems, err := loadSaleItemRows(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	returnItems, err := loadReturnItemRows(ctx, mdb, since, time.Time{})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	monthsMap := map[string]*models.MonthlyData{}
	for _, item := range saleItems {
		key := item.At.Format("2006-01")
		if monthsMap[key] == nil {
			monthsMap[key] = &models.MonthlyData{Month: key}
		}
		monthsMap[key].Revenue += item.Subtotal
		monthsMap[key].Cost += item.CostSubtotal
	}
	for _, item := range returnItems {
		key := item.At.Format("2006-01")
		if monthsMap[key] == nil {
			monthsMap[key] = &models.MonthlyData{Month: key}
		}
		monthsMap[key].Revenue -= item.Subtotal
		monthsMap[key].Cost -= item.CostSubtotal
	}

	keys := make([]string, 0, len(monthsMap))
	for key := range monthsMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]models.MonthlyData, 0, len(keys))
	for _, key := range keys {
		row := monthsMap[key]
		row.Profit = row.Revenue - row.Cost
		result = append(result, *row)
	}
	jsonOK(w, result)
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

	tz := loadTimezone(ctx, mdb)
	now := time.Now().In(tz)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
	endOfDay := startOfDay.Add(24 * time.Hour)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)
	sinceDaily := startOfDay.AddDate(0, 0, -days)
	sinceMonthly := now.AddDate(0, -12, 0)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		summary models.ReportSummary
		daily   []models.DailyData
		monthly []models.MonthlyData
		recent  []models.Sale
		firstErr error
	)
	setErr := func(e error) {
		mu.Lock()
		if firstErr == nil && e != nil {
			firstErr = e
		}
		mu.Unlock()
	}

	// (1) Summary
	wg.Add(1)
	go func() {
		defer wg.Done()
		todaySales, err := netSalesAmount(ctx, mdb, startOfDay, endOfDay)
		if err != nil { setErr(err); return }
		monthSales, err := netSalesAmount(ctx, mdb, startOfMonth, endOfDay)
		if err != nil { setErr(err); return }
		todayBills := countDocs(ctx, mdb, bson.M{"sold_at": bson.M{"$gte": startOfDay, "$lt": endOfDay}})
		stockValue, err := calcStockValue(ctx, mdb)
		if err != nil { setErr(err); return }
		lowThreshold := loadStockSettings(ctx, mdb).LowStockThreshold
		lowStock := int(countDrugs(ctx, mdb, bson.M{
			"$expr": bson.M{"$and": bson.A{
				bson.M{"$gt": bson.A{"$stock", 0}},
				bson.M{"$lte": bson.A{"$stock", bson.M{"$cond": bson.A{
					bson.M{"$gt": bson.A{"$min_stock", 0}}, "$min_stock", lowThreshold,
				}}}},
			}},
		}))
		outStock := int(countDrugs(ctx, mdb, bson.M{"stock": 0}))
		summary = models.ReportSummary{
			TodaySales: todaySales, TodayBills: int(todayBills), MonthSales: monthSales,
			StockValue: stockValue, LowStock: lowStock, OutStock: outStock,
		}
	}()

	// (2) Daily (last N days)
	wg.Add(1)
	go func() {
		defer wg.Done()
		saleItems, err := loadSaleItemRows(ctx, mdb, sinceDaily, time.Time{})
		if err != nil { setErr(err); return }
		returnItems, err := loadReturnItemRows(ctx, mdb, sinceDaily, time.Time{})
		if err != nil { setErr(err); return }
		dayTotals := map[string]float64{}
		for _, it := range saleItems { dayTotals[it.At.Format("2006-01-02")] += it.Subtotal }
		for _, it := range returnItems { dayTotals[it.At.Format("2006-01-02")] -= it.Subtotal }
		keys := make([]string, 0, len(dayTotals))
		for k := range dayTotals { keys = append(keys, k) }
		sort.Strings(keys)
		daily = make([]models.DailyData, 0, len(keys))
		for _, k := range keys {
			daily = append(daily, models.DailyData{Day: k, Total: dayTotals[k]})
		}
	}()

	// (3) Monthly (last 12 months)
	wg.Add(1)
	go func() {
		defer wg.Done()
		saleItems, err := loadSaleItemRows(ctx, mdb, sinceMonthly, time.Time{})
		if err != nil { setErr(err); return }
		returnItems, err := loadReturnItemRows(ctx, mdb, sinceMonthly, time.Time{})
		if err != nil { setErr(err); return }
		monthsMap := map[string]*models.MonthlyData{}
		for _, it := range saleItems {
			k := it.At.Format("2006-01")
			if monthsMap[k] == nil { monthsMap[k] = &models.MonthlyData{Month: k} }
			monthsMap[k].Revenue += it.Subtotal
			monthsMap[k].Cost += it.CostSubtotal
		}
		for _, it := range returnItems {
			k := it.At.Format("2006-01")
			if monthsMap[k] == nil { monthsMap[k] = &models.MonthlyData{Month: k} }
			monthsMap[k].Revenue -= it.Subtotal
			monthsMap[k].Cost -= it.CostSubtotal
		}
		keys := make([]string, 0, len(monthsMap))
		for k := range monthsMap { keys = append(keys, k) }
		sort.Strings(keys)
		monthly = make([]models.MonthlyData, 0, len(keys))
		for _, k := range keys {
			row := monthsMap[k]
			row.Profit = row.Revenue - row.Cost
			monthly = append(monthly, *row)
		}
	}()

	// (4) Recent 5 sales
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur, err := mdb.Sales().Find(ctx, bson.M{},
			options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(5),
		)
		if err != nil { setErr(err); return }
		defer cur.Close(ctx)
		var sales []models.Sale
		if err := cur.All(ctx, &sales); err != nil { setErr(err); return }
		if sales == nil { sales = []models.Sale{} }
		recent = sales
	}()

	wg.Wait()
	if firstErr != nil {
		jsonError(w, firstErr.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, models.Dashboard{
		Summary:     summary,
		Daily:       daily,
		Monthly:     monthly,
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

func resolveReportRange(r *http.Request, tz *time.Location) (time.Time, time.Time) {
	now := time.Now().In(tz)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)
	to := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, tz)

	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
			to = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, tz)
		}
	}
	return from, to
}

func loadSaleItemRows(ctx context.Context, d *db.MongoDB, from, to time.Time) ([]saleItemReportRow, error) {
	match := bson.M{}
	if !from.IsZero() || !to.IsZero() {
		dateFilter := bson.M{}
		if !from.IsZero() {
			dateFilter["$gte"] = from
		}
		if !to.IsZero() {
			dateFilter["$lte"] = to
		}
		match["sold_at"] = dateFilter
	}

	pipeline := bson.A{
		bson.M{"$match": notVoided(match)},
		bson.M{"$lookup": bson.M{
			"from":         "sale_items",
			"localField":   "_id",
			"foreignField": "sale_id",
			"as":           "items",
		}},
		bson.M{"$unwind": "$items"},
		bson.M{"$project": bson.M{
			"drug_id":       "$items.drug_id",
			"drug_name":     "$items.drug_name",
			"qty":           "$items.qty",
			"subtotal":      "$items.subtotal",
			"cost_subtotal": bson.M{"$ifNull": bson.A{"$items.cost_subtotal", 0}},
			"at":            "$sold_at",
		}},
	}

	cur, err := d.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var rows []saleItemReportRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func loadReturnItemRows(ctx context.Context, d *db.MongoDB, from, to time.Time) ([]returnItemReportRow, error) {
	match := bson.M{}
	if !from.IsZero() || !to.IsZero() {
		dateFilter := bson.M{}
		if !from.IsZero() {
			dateFilter["$gte"] = from
		}
		if !to.IsZero() {
			dateFilter["$lte"] = to
		}
		match["returned_at"] = dateFilter
	}

	pipeline := bson.A{
		bson.M{"$match": match},
		bson.M{"$unwind": "$items"},
		bson.M{"$project": bson.M{
			"drug_id":       "$items.drug_id",
			"drug_name":     "$items.drug_name",
			"qty":           "$items.qty",
			"subtotal":      "$items.subtotal",
			"cost_subtotal": bson.M{"$ifNull": bson.A{"$items.cost_subtotal", 0}},
			"at":            "$returned_at",
		}},
	}

	cur, err := d.DrugReturns().Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var rows []returnItemReportRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func netTotalsByDrug(ctx context.Context, d *db.MongoDB, from, to time.Time) (map[bson.ObjectID]*netDrugTotals, error) {
	saleItems, err := loadSaleItemRows(ctx, d, from, to)
	if err != nil {
		return nil, err
	}
	returnItems, err := loadReturnItemRows(ctx, d, from, to)
	if err != nil {
		return nil, err
	}

	totals := map[bson.ObjectID]*netDrugTotals{}
	for _, item := range saleItems {
		if totals[item.DrugID] == nil {
			totals[item.DrugID] = &netDrugTotals{DrugName: item.DrugName}
		}
		totals[item.DrugID].Qty += item.Qty
		totals[item.DrugID].Revenue += item.Subtotal
		totals[item.DrugID].Cost += item.CostSubtotal
	}
	for _, item := range returnItems {
		if totals[item.DrugID] == nil {
			totals[item.DrugID] = &netDrugTotals{DrugName: item.DrugName}
		}
		totals[item.DrugID].Qty -= item.Qty
		totals[item.DrugID].Revenue -= item.Subtotal
		totals[item.DrugID].Cost -= item.CostSubtotal
	}
	return totals, nil
}

func netSalesAmount(ctx context.Context, d *db.MongoDB, from, to time.Time) (float64, error) {
	sales, err := sumSales(ctx, d, bson.M{"sold_at": bson.M{"$gte": from, "$lt": to}})
	if err != nil {
		return 0, err
	}
	returns, err := sumReturnRefunds(ctx, d, from, to)
	if err != nil {
		return 0, err
	}
	return sales - returns, nil
}

func sumSales(ctx context.Context, d *db.MongoDB, filter bson.M) (float64, error) {
	pipeline := bson.A{
		bson.M{"$match": notVoided(filter)},
		bson.M{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": "$total"}}},
	}
	cur, err := d.Sales().Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var res []struct {
		Total float64 `bson:"total"`
	}
	if err := cur.All(ctx, &res); err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0].Total, nil
}

func sumReturnRefunds(ctx context.Context, d *db.MongoDB, from, to time.Time) (float64, error) {
	pipeline := bson.A{
		bson.M{"$match": bson.M{"returned_at": bson.M{"$gte": from, "$lt": to}}},
		bson.M{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": "$refund"}}},
	}
	cur, err := d.DrugReturns().Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var res []struct {
		Total float64 `bson:"total"`
	}
	if err := cur.All(ctx, &res); err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0].Total, nil
}

func countDocs(ctx context.Context, d *db.MongoDB, filter bson.M) int64 {
	n, _ := d.Sales().CountDocuments(ctx, notVoided(filter))
	return n
}

func countDrugs(ctx context.Context, d *db.MongoDB, filter bson.M) int64 {
	n, _ := d.Drugs().CountDocuments(ctx, filter)
	return n
}

func calcStockValue(ctx context.Context, d *db.MongoDB) (float64, error) {
	pipeline := bson.A{
		bson.M{"$group": bson.M{
			"_id":   nil,
			"total": bson.M{"$sum": bson.M{"$multiply": bson.A{"$cost_price", "$stock"}}},
		}},
	}
	cur, err := d.Drugs().Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var res []struct {
		Total float64 `bson:"total"`
	}
	if err := cur.All(ctx, &res); err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0].Total, nil
}
