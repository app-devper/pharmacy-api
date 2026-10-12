// Package reporting owns what a period's sales add up to (ADR-0008), for every
// report and for the End-of-day close:
//
//   - a period is [from, to) and days and months are calendar dates in the
//     pharmacy's timezone;
//   - a sale counts when it was confirmed (sold_at), unless voided; a return
//     counts when it was made;
//   - an amount is what the customer paid: a bill discount is spread over the
//     bill's lines in proportion to their subtotals, and a return refunds its
//     lines' paid share.
//
// So a day's line totals equal its bill totals less refunds, which is the
// End-of-day figure.
package reporting

import (
	"context"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

const (
	dayLayout   = "2006-01-02"
	monthLayout = "2006-01"
)

// DayStart is midnight of t's calendar day in tz.
func DayStart(t time.Time, tz *time.Location) time.Time {
	t = t.In(tz)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz)
}

// monthStart is midnight of the first day of t's month in tz.
func monthStart(t time.Time, tz *time.Location) time.Time {
	t = t.In(tz)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, tz)
}

func period(field string, from, to time.Time) bson.M {
	f := bson.M{}
	if !from.IsZero() {
		f["$gte"] = from
	}
	if !to.IsZero() {
		f["$lt"] = to
	}
	if len(f) == 0 {
		return bson.M{}
	}
	return bson.M{field: f}
}

func confirmedSales(from, to time.Time) bson.M {
	m := period("sold_at", from, to)
	m["voided"] = bson.M{"$ne": true}
	return m
}

// line is one drug line of a sale (positive) or a return (negative), valued
// at what the customer paid.
type line struct {
	DrugID   bson.ObjectID `bson:"drug_id"`
	DrugName string        `bson:"drug_name"`
	Qty      int           `bson:"qty"`
	Revenue  float64       `bson:"revenue"`
	Cost     float64       `bson:"cost"`
	At       time.Time     `bson:"at"`
}

// lines loads every sale and return line in [from, to); a zero bound is open.
func lines(ctx context.Context, mdb *db.MongoDB, from, to time.Time) ([]line, error) {
	// A bill's lines add up to total + discount; each line keeps its share of total.
	paidShare := bson.M{"$cond": bson.A{
		bson.M{"$gt": bson.A{bson.M{"$add": bson.A{"$total", "$discount"}}, 0}},
		bson.M{"$divide": bson.A{"$total", bson.M{"$add": bson.A{"$total", "$discount"}}}},
		1,
	}}
	sales, err := aggregate(ctx, mdb.Sales(), bson.A{
		bson.M{"$match": confirmedSales(from, to)},
		bson.M{"$lookup": bson.M{"from": "sale_items", "localField": "_id", "foreignField": "sale_id", "as": "items"}},
		bson.M{"$addFields": bson.M{"share": paidShare}},
		bson.M{"$unwind": "$items"},
		bson.M{"$project": bson.M{
			"drug_id":   "$items.drug_id",
			"drug_name": "$items.drug_name",
			"qty":       "$items.qty",
			"revenue":   bson.M{"$multiply": bson.A{"$items.subtotal", "$share"}},
			"cost":      bson.M{"$ifNull": bson.A{"$items.cost_subtotal", 0}},
			"at":        "$sold_at",
		}},
	})
	if err != nil {
		return nil, err
	}
	returns, err := aggregate(ctx, mdb.DrugReturns(), bson.A{
		bson.M{"$match": period("returned_at", from, to)},
		bson.M{"$unwind": "$items"},
		bson.M{"$project": bson.M{
			"drug_id":   "$items.drug_id",
			"drug_name": "$items.drug_name",
			"qty":       bson.M{"$multiply": bson.A{"$items.qty", -1}},
			"revenue":   bson.M{"$multiply": bson.A{"$items.subtotal", -1}},
			"cost":      bson.M{"$multiply": bson.A{bson.M{"$ifNull": bson.A{"$items.cost_subtotal", 0}}, -1}},
			"at":        "$returned_at",
		}},
	})
	if err != nil {
		return nil, err
	}
	return append(sales, returns...), nil
}

func aggregate(ctx context.Context, c *mongo.Collection, pipeline bson.A) ([]line, error) {
	cur, err := c.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var out []line
	err = cur.All(ctx, &out)
	return out, err
}

// daily totals lines by calendar day in tz, oldest first.
func daily(lines []line, tz *time.Location) []models.DailyData {
	totals := map[string]float64{}
	for _, l := range lines {
		totals[l.At.In(tz).Format(dayLayout)] += l.Revenue
	}
	keys := sortedKeys(totals)
	out := make([]models.DailyData, 0, len(keys))
	for _, k := range keys {
		out = append(out, models.DailyData{Day: k, Total: totals[k]})
	}
	return out
}

// monthly totals revenue, cost and profit by calendar month in tz.
func monthly(lines []line, tz *time.Location) []models.MonthlyData {
	byMonth := map[string]*models.MonthlyData{}
	for _, l := range lines {
		k := l.At.In(tz).Format(monthLayout)
		if byMonth[k] == nil {
			byMonth[k] = &models.MonthlyData{Month: k}
		}
		byMonth[k].Revenue += l.Revenue
		byMonth[k].Cost += l.Cost
	}
	keys := make([]string, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]models.MonthlyData, 0, len(keys))
	for _, k := range keys {
		row := byMonth[k]
		row.Profit = row.Revenue - row.Cost
		out = append(out, *row)
	}
	return out
}

// drugTotals is a drug's net quantity, revenue and cost over some lines.
type drugTotals struct {
	DrugName string
	Qty      int
	Revenue  float64
	Cost     float64
}

// byDrug totals lines per drug.
func byDrug(lines []line) map[bson.ObjectID]*drugTotals {
	out := map[bson.ObjectID]*drugTotals{}
	for _, l := range lines {
		t := out[l.DrugID]
		if t == nil {
			t = &drugTotals{DrugName: l.DrugName}
			out[l.DrugID] = t
		}
		t.Qty += l.Qty
		t.Revenue += l.Revenue
		t.Cost += l.Cost
	}
	return out
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// netSales is confirmed bill totals less refunds over [from, to).
func netSales(ctx context.Context, mdb *db.MongoDB, from, to time.Time) (float64, error) {
	sales, err := sum(ctx, mdb.Sales(), confirmedSales(from, to), "$total")
	if err != nil {
		return 0, err
	}
	refunds, err := sum(ctx, mdb.DrugReturns(), period("returned_at", from, to), "$refund")
	return sales - refunds, err
}

// bills counts confirmed bills over [from, to).
func bills(ctx context.Context, mdb *db.MongoDB, from, to time.Time) (int, error) {
	n, err := mdb.Sales().CountDocuments(ctx, confirmedSales(from, to))
	return int(n), err
}

func sum(ctx context.Context, c *mongo.Collection, match bson.M, field string) (float64, error) {
	cur, err := c.Aggregate(ctx, bson.A{
		bson.M{"$match": match},
		bson.M{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": field}}},
	})
	if err != nil {
		return 0, err
	}
	var res []struct {
		Total float64 `bson:"total"`
	}
	if err := cur.All(ctx, &res); err != nil || len(res) == 0 {
		return 0, err
	}
	return res[0].Total, nil
}

// Day is a business day's End-of-day report computed from its confirmed
// bills and the refunds of returns made that day.
func Day(ctx context.Context, mdb *db.MongoDB, day time.Time, tz *time.Location) (models.EodReport, error) {
	start := DayStart(day, tz)
	end := start.AddDate(0, 0, 1)
	cur, err := mdb.Sales().Find(ctx, confirmedSales(start, end), options.Find().SetSort(bson.D{{Key: "sold_at", Value: 1}}))
	if err != nil {
		return models.EodReport{}, err
	}
	bills := []models.Sale{}
	if err := cur.All(ctx, &bills); err != nil {
		return models.EodReport{}, err
	}
	refunds, err := sum(ctx, mdb.DrugReturns(), period("returned_at", start, end), "$refund")
	if err != nil {
		return models.EodReport{}, err
	}
	var sales, discount, received, change float64
	for _, b := range bills {
		sales += b.Total
		discount += b.Discount
		received += b.Received
		change += b.Change
	}
	return models.EodReport{
		Date:          start.Format(dayLayout),
		BillCount:     len(bills),
		TotalSales:    sales - refunds,
		TotalDiscount: discount,
		TotalReceived: received,
		TotalChange:   change,
		NetCash:       received - change - refunds,
		Bills:         bills,
	}, nil
}
