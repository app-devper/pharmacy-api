package reporting

import (
	"context"
	"math"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	"pharmacy-pos/backend/models"
)

// The named reports. Each takes the moment it is asked at (now) and the
// window its caller chose, and resolves days and months in the pharmacy's
// timezone, so callers never do date maths.

// lastDays is [start of the day `days` days before today, open).
func lastDays(now time.Time, tz *time.Location, days int) time.Time {
	return DayStart(now, tz).AddDate(0, 0, -days)
}

// lastMonths is the start of the month (months-1) months back, so the window
// covers `months` calendar months including the current one.
func lastMonths(now time.Time, tz *time.Location, months int) time.Time {
	return monthStart(now, tz).AddDate(0, -(months - 1), 0)
}

// DateRange is [from, to) from inclusive YYYY-MM-DD dates in the pharmacy's
// calendar. An empty or malformed from is the first of this month; an empty
// or malformed to is today.
func DateRange(now time.Time, tz *time.Location, from, to string) (time.Time, time.Time) {
	start := monthStart(now, tz)
	end := DayStart(now, tz).AddDate(0, 0, 1)
	if t, err := time.ParseInLocation(dayLayout, from, tz); err == nil {
		start = t
	}
	if t, err := time.ParseInLocation(dayLayout, to, tz); err == nil {
		end = t.AddDate(0, 0, 1)
	}
	return start, end
}

// Summary is today's and this month's net sales, today's bills, and the
// stock summary. lowStock is the tenant's default low-stock threshold.
func Summary(ctx context.Context, mdb *db.MongoDB, now time.Time, lowStock int) (models.ReportSummary, error) {
	tz := mdb.Timezone(ctx)
	return summary(ctx, mdb, now, tz, lowStock)
}

func summary(ctx context.Context, mdb *db.MongoDB, now time.Time, tz *time.Location, lowStock int) (models.ReportSummary, error) {
	today := DayStart(now, tz)
	tomorrow := today.AddDate(0, 0, 1)
	todaySales, err := netSales(ctx, mdb, today, tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	monthSales, err := netSales(ctx, mdb, monthStart(now, tz), tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	todayBills, err := bills(ctx, mdb, today, tomorrow)
	if err != nil {
		return models.ReportSummary{}, err
	}
	stock, err := inventory.Summarise(ctx, mdb, lowStock)
	if err != nil {
		return models.ReportSummary{}, err
	}
	return models.ReportSummary{
		TodaySales: todaySales, TodayBills: todayBills, MonthSales: monthSales,
		StockValue: stock.Value, LowStock: stock.Low, OutStock: stock.Out,
	}, nil
}

// Daily is net sales per day over the last `days` days and today.
func Daily(ctx context.Context, mdb *db.MongoDB, now time.Time, days int) ([]models.DailyData, error) {
	tz := mdb.Timezone(ctx)
	ls, err := lines(ctx, mdb, lastDays(now, tz, days), time.Time{})
	if err != nil {
		return nil, err
	}
	return daily(ls, tz), nil
}

// Monthly is revenue, cost and profit per month over `months` calendar
// months including this one.
func Monthly(ctx context.Context, mdb *db.MongoDB, now time.Time, months int) ([]models.MonthlyData, error) {
	tz := mdb.Timezone(ctx)
	ls, err := lines(ctx, mdb, lastMonths(now, tz, months), time.Time{})
	if err != nil {
		return nil, err
	}
	return monthly(ls, tz), nil
}

// Profit is revenue, cost, profit and margin per drug and in total over the
// inclusive dates from..to (see DateRange), most profitable drug first.
func Profit(ctx context.Context, mdb *db.MongoDB, now time.Time, from, to string) (models.ProfitReport, error) {
	start, end := DateRange(now, mdb.Timezone(ctx), from, to)
	ls, err := lines(ctx, mdb, start, end)
	if err != nil {
		return models.ProfitReport{}, err
	}
	totals := byDrug(ls)
	rows := make([]models.DrugProfit, 0, len(totals))
	var total models.ProfitSummary
	for drugID, t := range totals {
		profit := t.Revenue - t.Cost
		rows = append(rows, models.DrugProfit{
			DrugID:   drugID.Hex(),
			DrugName: t.DrugName,
			QtySold:  t.Qty,
			Revenue:  t.Revenue,
			Cost:     t.Cost,
			Profit:   profit,
			Margin:   margin(profit, t.Revenue),
		})
		total.Revenue += t.Revenue
		total.Cost += t.Cost
		total.Profit += profit
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Profit != rows[j].Profit {
			return rows[i].Profit > rows[j].Profit
		}
		return rows[i].DrugID < rows[j].DrugID
	})
	total.Margin = margin(total.Profit, total.Revenue)
	if total.Bills, err = bills(ctx, mdb, start, end); err != nil {
		return models.ProfitReport{}, err
	}
	return models.ProfitReport{Summary: total, ByDrug: rows}, nil
}

// margin is profit as a percentage of revenue, 0 without revenue.
func margin(profit, revenue float64) float64 {
	if revenue <= 0 {
		return 0
	}
	return profit / revenue * 100
}

// TopDrugsLimit is how many drugs TopDrugs returns.
const TopDrugsLimit = 10

// TopDrugs is the best-selling drugs by net quantity over the last `days`
// days and today. A drug whose sales were all returned is left out.
func TopDrugs(ctx context.Context, mdb *db.MongoDB, now time.Time, days int) ([]models.TopDrug, error) {
	ls, err := lines(ctx, mdb, lastDays(now, mdb.Timezone(ctx), days), time.Time{})
	if err != nil {
		return nil, err
	}
	out := []models.TopDrug{}
	for drugID, t := range byDrug(ls) {
		if t.Qty <= 0 && t.Revenue <= 0 {
			continue
		}
		out = append(out, models.TopDrug{DrugID: drugID.Hex(), DrugName: t.DrugName, QtySold: t.Qty, Revenue: t.Revenue})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QtySold != out[j].QtySold {
			return out[i].QtySold > out[j].QtySold
		}
		return out[i].DrugID < out[j].DrugID
	})
	if len(out) > TopDrugsLimit {
		out = out[:TopDrugsLimit]
	}
	return out, nil
}

// SlowDrugsLimit is how many drugs SlowDrugs returns.
const SlowDrugsLimit = 30

// SlowDrugs is drugs in stock that no confirmed sale took over the last
// `days` days and today, largest stock first.
func SlowDrugs(ctx context.Context, mdb *db.MongoDB, now time.Time, days int) ([]models.SlowDrug, error) {
	ls, err := lines(ctx, mdb, lastDays(now, mdb.Timezone(ctx), days), time.Time{})
	if err != nil {
		return nil, err
	}
	sold := []bson.ObjectID{}
	seen := map[bson.ObjectID]bool{}
	for _, l := range ls {
		if l.Qty > 0 && !seen[l.DrugID] {
			seen[l.DrugID] = true
			sold = append(sold, l.DrugID)
		}
	}
	cur, err := mdb.Drugs().Find(ctx,
		bson.M{"stock": bson.M{"$gt": 0}, "_id": bson.M{"$nin": sold}},
		options.Find().SetSort(bson.D{{Key: "stock", Value: -1}, {Key: "_id", Value: 1}}).SetLimit(SlowDrugsLimit),
	)
	if err != nil {
		return nil, err
	}
	out := []models.SlowDrug{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Reorder suggests how much of each drug to order: the average daily net
// quantity sold over the last `days` days, projected over `lookahead` days,
// less the stock on hand. Only drugs that sold are considered. Out of stock
// first, then the fewest days of stock left.
func Reorder(ctx context.Context, mdb *db.MongoDB, now time.Time, days, lookahead int) ([]models.ReorderSuggestion, error) {
	ls, err := lines(ctx, mdb, lastDays(now, mdb.Timezone(ctx), days), time.Time{})
	if err != nil {
		return nil, err
	}
	totals := byDrug(ls)
	ids := make([]bson.ObjectID, 0, len(totals))
	for id, t := range totals {
		if t.Qty > 0 {
			ids = append(ids, id)
		}
	}
	out := []models.ReorderSuggestion{}
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := mdb.Drugs().Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var drugs []models.Drug
	if err := cur.All(ctx, &drugs); err != nil {
		return nil, err
	}
	for _, d := range drugs {
		avgDaily := float64(totals[d.ID].Qty) / float64(days)
		need := int(math.Ceil(avgDaily * float64(lookahead)))
		if d.Stock >= need {
			continue
		}
		out = append(out, models.ReorderSuggestion{
			DrugID:       d.ID.Hex(),
			DrugName:     d.Name,
			Unit:         d.Unit,
			CurrentStock: d.Stock,
			MinStock:     d.MinStock,
			QtySold:      totals[d.ID].Qty,
			AvgDailySale: avgDaily,
			DaysLeft:     float64(d.Stock) / avgDaily,
			SuggestedQty: need - d.Stock,
			CostPrice:    d.CostPrice,
			SellPrice:    d.SellPrice,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].CurrentStock == 0) != (out[j].CurrentStock == 0) {
			return out[i].CurrentStock == 0
		}
		if out[i].DaysLeft != out[j].DaysLeft {
			return out[i].DaysLeft < out[j].DaysLeft
		}
		return out[i].DrugID < out[j].DrugID
	})
	return out, nil
}

const (
	dashboardMonths = 12
	recentSales     = 5
)

// Dashboard is the summary, the daily chart over `days` days, the monthly
// chart over 12 months, and the five latest confirmed bills.
func Dashboard(ctx context.Context, mdb *db.MongoDB, now time.Time, days, lowStock int) (models.Dashboard, error) {
	tz := mdb.Timezone(ctx)
	head, err := summary(ctx, mdb, now, tz, lowStock)
	if err != nil {
		return models.Dashboard{}, err
	}
	dailyFrom, monthlyFrom := lastDays(now, tz, days), lastMonths(now, tz, dashboardMonths)
	ls, err := lines(ctx, mdb, earlier(dailyFrom, monthlyFrom), time.Time{})
	if err != nil {
		return models.Dashboard{}, err
	}
	var dailyLines, monthlyLines []line
	for _, l := range ls {
		if !l.At.Before(dailyFrom) {
			dailyLines = append(dailyLines, l)
		}
		if !l.At.Before(monthlyFrom) {
			monthlyLines = append(monthlyLines, l)
		}
	}
	cur, err := mdb.Sales().Find(ctx, confirmedSales(time.Time{}, time.Time{}),
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(recentSales))
	if err != nil {
		return models.Dashboard{}, err
	}
	recent := []models.Sale{}
	if err := cur.All(ctx, &recent); err != nil {
		return models.Dashboard{}, err
	}
	return models.Dashboard{
		Summary:     head,
		Daily:       daily(dailyLines, tz),
		Monthly:     monthly(monthlyLines, tz),
		RecentSales: recent,
	}, nil
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
