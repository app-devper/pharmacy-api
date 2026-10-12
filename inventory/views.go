package inventory

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Views of stock. They answer with the same rules the commands use (which
// lots a sale may take from, first expiry first), so what the pharmacy sees
// is what a sale will do (ADR-0010).

// NextLots returns, per drug, the lot the next sale takes from: the first
// sellable lot by expiry. Drugs with no sellable lot are absent.
func NextLots(ctx context.Context, mdb *db.MongoDB) (map[bson.ObjectID]models.LotSummary, error) {
	cur, err := mdb.DrugLots().Aggregate(ctx, bson.A{
		bson.M{"$match": sellableLots()},
		bson.M{"$sort": bson.D{{Key: "drug_id", Value: 1}, {Key: "expiry_date", Value: 1}, {Key: "_id", Value: 1}}},
		bson.M{"$group": bson.M{
			"_id":         "$drug_id",
			"lot_id":      bson.M{"$first": "$_id"},
			"lot_number":  bson.M{"$first": "$lot_number"},
			"expiry_date": bson.M{"$first": "$expiry_date"},
		}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		DrugID     bson.ObjectID `bson:"_id"`
		LotID      bson.ObjectID `bson:"lot_id"`
		LotNumber  string        `bson:"lot_number"`
		ExpiryDate time.Time     `bson:"expiry_date"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[bson.ObjectID]models.LotSummary, len(rows))
	for _, r := range rows {
		out[r.DrugID] = models.LotSummary{LotID: r.LotID, LotNumber: r.LotNumber, ExpiryDate: r.ExpiryDate}
	}
	return out, nil
}

// Lots returns every lot of a drug, written off or empty included, in the
// order sales take from them.
func Lots(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID) ([]models.DrugLot, error) {
	cur, err := mdb.DrugLots().Find(ctx, bson.M{"drug_id": drugID}, byExpiry)
	if err != nil {
		return nil, err
	}
	lots := []models.DrugLot{}
	if err := cur.All(ctx, &lots); err != nil {
		return nil, err
	}
	return lots, nil
}

// Expiring returns sellable lots that expire within days of now, already
// expired ones included; expiredOnly keeps just those past expiry. Lots
// marked no_expiry never appear.
func Expiring(ctx context.Context, mdb *db.MongoDB, now time.Time, days int, expiredOnly bool) ([]models.ExpiringLotItem, error) {
	filter := sellableLots()
	filter["no_expiry"] = bson.M{"$ne": true}
	if expiredOnly {
		filter["expiry_date"] = bson.M{"$lt": now}
	} else {
		filter["expiry_date"] = bson.M{"$lte": now.AddDate(0, 0, days)}
	}
	cur, err := mdb.DrugLots().Find(ctx, filter, byExpiry)
	if err != nil {
		return nil, err
	}
	var lots []models.DrugLot
	if err := cur.All(ctx, &lots); err != nil {
		return nil, err
	}
	out := make([]models.ExpiringLotItem, 0, len(lots))
	for _, l := range lots {
		out = append(out, models.ExpiringLotItem{
			ID:         l.ID,
			DrugID:     l.DrugID,
			DrugName:   l.DrugName,
			LotNumber:  l.LotNumber,
			ExpiryDate: l.ExpiryDate,
			Remaining:  l.Remaining,
			DaysLeft:   int(l.ExpiryDate.Sub(now).Hours() / 24),
		})
	}
	return out, nil
}

// lowStock matches drugs that are nearly out: stock above zero and at or
// below the drug's own min_stock, or threshold when it has none. A drug at
// or below zero is out of stock instead.
func lowStock(threshold int) bson.M {
	return bson.M{"$expr": bson.M{"$and": bson.A{
		bson.M{"$gt": bson.A{"$stock", 0}},
		bson.M{"$lte": bson.A{"$stock", bson.M{"$cond": bson.A{
			bson.M{"$gt": bson.A{"$min_stock", 0}}, "$min_stock", threshold,
		}}}},
	}}}
}

// LowStock returns the drugs that are nearly out, lowest stock first.
func LowStock(ctx context.Context, mdb *db.MongoDB, threshold int) ([]models.Drug, error) {
	cur, err := mdb.Drugs().Find(ctx, lowStock(threshold), options.Find().SetSort(bson.D{{Key: "stock", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	drugs := []models.Drug{}
	if err := cur.All(ctx, &drugs); err != nil {
		return nil, err
	}
	return drugs, nil
}

// StockSummary is the dashboard's view of stock.
type StockSummary struct {
	Value float64 // cost of the units on hand; oversold drugs count as zero
	Low   int     // nearly out (see LowStock)
	Out   int     // at or below zero
}

// Summarise counts low and out-of-stock drugs with the same rule LowStock
// lists them by, and values the units on hand.
func Summarise(ctx context.Context, mdb *db.MongoDB, threshold int) (StockSummary, error) {
	var sum StockSummary
	cur, err := mdb.Drugs().Aggregate(ctx, bson.A{
		bson.M{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": bson.M{"$multiply": bson.A{
			"$cost_price", bson.M{"$max": bson.A{"$stock", 0}},
		}}}}},
	})
	if err != nil {
		return sum, err
	}
	var res []struct {
		Total float64 `bson:"total"`
	}
	if err := cur.All(ctx, &res); err != nil {
		return sum, err
	}
	if len(res) > 0 {
		sum.Value = res[0].Total
	}
	low, err := mdb.Drugs().CountDocuments(ctx, lowStock(threshold))
	if err != nil {
		return sum, err
	}
	out, err := mdb.Drugs().CountDocuments(ctx, bson.M{"stock": bson.M{"$lte": 0}})
	if err != nil {
		return sum, err
	}
	sum.Low, sum.Out = int(low), int(out)
	return sum, nil
}
