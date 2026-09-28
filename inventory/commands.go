package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

const dateLayout = "2006-01-02"

// AddLot receives a lot entered by hand: like a goods receipt, it settles
// oversold debt from the new lot.
func AddLot(ctx context.Context, mdb *db.MongoDB, drugID string, in models.DrugLotInput) (models.DrugLot, error) {
	drugOID, err := bson.ObjectIDFromHex(drugID)
	if err != nil {
		return models.DrugLot{}, refusal.Invalidf("invalid drug id")
	}
	switch {
	case in.LotNumber == "":
		return models.DrugLot{}, refusal.Invalidf("lot_number is required")
	case in.Quantity <= 0:
		return models.DrugLot{}, refusal.Invalidf("quantity must be > 0")
	case in.ExpiryDate == "":
		return models.DrugLot{}, refusal.Invalidf("expiry_date is required")
	}
	tz := mdb.Timezone(ctx)
	expiry, err := time.ParseInLocation(dateLayout, in.ExpiryDate, tz)
	if err != nil {
		return models.DrugLot{}, refusal.Invalidf("expiry_date must be YYYY-MM-DD")
	}
	importDate := time.Now()
	if in.ImportDate != "" {
		if importDate, err = time.ParseInLocation(dateLayout, in.ImportDate, tz); err != nil {
			return models.DrugLot{}, refusal.Invalidf("import_date must be YYYY-MM-DD")
		}
	}
	drug, err := loadDrug(ctx, mdb, drugOID)
	if err != nil {
		return models.DrugLot{}, err
	}
	var lot models.DrugLot
	err = mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		lot, err = ReceiveLot(txCtx, mdb, models.DrugLot{
			DrugID: drugOID, DrugName: drug.Name, LotNumber: in.LotNumber, ExpiryDate: expiry,
			ImportDate: importDate, CostPrice: in.CostPrice, SellPrice: in.SellPrice, Quantity: in.Quantity,
		})
		return err
	})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.DrugLot{}, refusal.NotFoundf("drug not found")
	}
	return lot, err
}

// OpeningLot is the lot for stock imported without lot data (bulk import):
// no expiry, taken first.
func OpeningLot(drug models.Drug) models.DrugLot {
	now := time.Now()
	return models.DrugLot{
		DrugID: drug.ID, DrugName: drug.Name, LotNumber: "OPENING", NoExpiry: true,
		ImportDate: now, CostPrice: &drug.CostPrice, Quantity: drug.Stock, Remaining: drug.Stock, CreatedAt: now,
		Origin: models.LotOpening,
	}
}

// OpenStock records the lot a new drug's stock is in. The drug was inserted
// with that stock already, so only the lot is written. Call inside the
// transaction that inserts the drug.
func OpenStock(txCtx context.Context, mdb *db.MongoDB, lot models.DrugLot) error {
	lot.Origin = models.LotOpening
	if lot.CreatedAt.IsZero() {
		lot.CreatedAt = time.Now()
	}
	_, err := mdb.DrugLots().InsertOne(txCtx, lot)
	return err
}

// Adjust changes a drug's stock by in.Delta and records why.
func Adjust(ctx context.Context, mdb *db.MongoDB, drugID string, in models.StockAdjustmentInput) (models.Drug, error) {
	oid, err := bson.ObjectIDFromHex(drugID)
	if err != nil {
		return models.Drug{}, refusal.Invalidf("invalid id")
	}
	switch {
	case in.Delta == 0:
		return models.Drug{}, refusal.Invalidf("delta must not be zero")
	case in.Reason == "":
		return models.Drug{}, refusal.Invalidf("reason is required")
	case !slices.Contains(models.AdjustmentReasons, in.Reason):
		return models.Drug{}, refusal.Invalidf("invalid reason")
	}
	tz := mdb.Timezone(ctx)
	var drug models.Drug
	err = mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		if _, err := adjust(txCtx, mdb, tz, oid, in.Delta, in.Lot, in.Reason, in.Note, time.Now()); err != nil {
			return err
		}
		drug, err = loadDrug(txCtx, mdb, oid)
		return err
	})
	return drug, err
}

// adjust applies delta to a drug inside a transaction and writes its audit
// record. A decrease takes from lots first expiry first; an increase settles
// oversold debt and goes into the named lot, or the latest-expiring one
// (flagged lot_assumed) when none is named.
func adjust(txCtx context.Context, mdb *db.MongoDB, tz *time.Location, drugID bson.ObjectID, delta int, target *models.LotTarget, reason, note string, at time.Time) (models.StockAdjustment, error) {
	drug, err := loadDrug(txCtx, mdb, drugID)
	if err != nil {
		return models.StockAdjustment{}, err
	}
	rec := models.StockAdjustment{
		DrugID: drugID, DrugName: drug.Name, Delta: delta, Before: drug.Stock, After: drug.Stock + delta,
		Reason: reason, Note: note, CreatedAt: at, By: actor(txCtx),
	}
	if delta < 0 && drug.Stock < -delta {
		return models.StockAdjustment{}, refusal.Invalidf("drug not found or insufficient stock")
	}
	if _, err := mdb.Drugs().UpdateOne(txCtx, bson.M{"_id": drugID}, bson.M{"$inc": bson.M{"stock": delta}}); err != nil {
		return models.StockAdjustment{}, err
	}

	if delta < 0 {
		rec.Lots, err = takeFromLots(txCtx, mdb, drugID, -delta)
	} else {
		rec.Lots, rec.LotAssumed, err = addToLot(txCtx, mdb, tz, drug, delta, target, reason)
	}
	if err != nil {
		return models.StockAdjustment{}, err
	}
	_, err = mdb.StockAdjustments().InsertOne(txCtx, rec)
	return rec, err
}

// takeFromLots removes up to qty units from sellable lots, first expiry
// first. Units no lot covers come off stock alone: that only happens to a
// drug whose stock already exceeded its lots, and moves it toward them.
func takeFromLots(txCtx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, qty int) ([]models.LotDeduction, error) {
	cur, err := mdb.DrugLots().Find(txCtx, sellable(drugID), byExpiry)
	if err != nil {
		return nil, err
	}
	var lots []models.DrugLot
	if err := cur.All(txCtx, &lots); err != nil {
		return nil, err
	}
	var used []models.LotDeduction
	for _, lot := range lots {
		if qty <= 0 {
			break
		}
		take := min(lot.Remaining, qty)
		if _, err := mdb.DrugLots().UpdateOne(txCtx, bson.M{"_id": lot.ID}, bson.M{"$inc": bson.M{"remaining": -take}}); err != nil {
			return nil, err
		}
		used = append(used, models.LotDeduction{LotID: lot.ID, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate, Qty: -take})
		qty -= take
	}
	return used, nil
}

func addToLot(txCtx context.Context, mdb *db.MongoDB, tz *time.Location, drug models.Drug, qty int, target *models.LotTarget, reason string) ([]models.LotDeduction, bool, error) {
	tracked, err := hasLots(txCtx, mdb, drug.ID)
	if err != nil {
		return nil, false, err
	}
	if !tracked {
		_, err := settleOversold(txCtx, mdb, drug.ID, nil, qty, reason)
		return nil, false, err
	}

	var lot models.DrugLot
	assumed := false
	switch {
	case target != nil && target.LotID != "":
		lotID, err := bson.ObjectIDFromHex(target.LotID)
		if err != nil {
			return nil, false, refusal.Invalidf("invalid lot id")
		}
		err = mdb.DrugLots().FindOne(txCtx, bson.M{"_id": lotID, "drug_id": drug.ID}).Decode(&lot)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, false, refusal.NotFoundf("lot not found")
		}
		if err != nil {
			return nil, false, err
		}
		if lot.WrittenOffAt != nil {
			return nil, false, refusal.Conflictf("lot is written off")
		}
	case target != nil && target.LotNumber != "":
		expiry, err := time.ParseInLocation(dateLayout, target.ExpiryDate, tz)
		if err != nil {
			return nil, false, refusal.Invalidf("expiry_date must be YYYY-MM-DD for a new lot")
		}
		cost := drug.CostPrice
		lot = models.DrugLot{DrugID: drug.ID, DrugName: drug.Name, LotNumber: target.LotNumber, ExpiryDate: expiry,
			ImportDate: time.Now(), CostPrice: &cost, CreatedAt: time.Now(), Origin: models.LotAdjustment}
		res, err := mdb.DrugLots().InsertOne(txCtx, lot)
		if err != nil {
			return nil, false, err
		}
		lot.ID = res.InsertedID.(bson.ObjectID)
	default:
		// Transition until clients name the lot (ADR-0007): the latest-expiring
		// lot still in use, flagged for review.
		err := mdb.DrugLots().FindOne(txCtx,
			bson.M{"drug_id": drug.ID, "written_off_at": bson.M{"$exists": false}},
			options.FindOne().SetSort(bson.D{{Key: "expiry_date", Value: -1}}),
		).Decode(&lot)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, false, refusal.Invalidf("name the lot (lot_id, or lot_number and expiry_date) for this increase")
		}
		if err != nil {
			return nil, false, err
		}
		assumed = true
	}

	if _, err := mdb.DrugLots().UpdateOne(txCtx, bson.M{"_id": lot.ID},
		bson.M{"$inc": bson.M{"remaining": qty, "quantity": qtyIfNew(lot, qty)}}); err != nil {
		return nil, false, err
	}
	lot.Remaining += qty
	if _, err := settleOversold(txCtx, mdb, drug.ID, &lot, qty, ""); err != nil {
		return nil, false, err
	}
	return []models.LotDeduction{{LotID: lot.ID, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate, Qty: qty}}, assumed, nil
}

// qtyIfNew is the quantity to record on a lot just created by an increase;
// an existing lot keeps its original received quantity.
func qtyIfNew(lot models.DrugLot, qty int) int {
	if lot.Quantity == 0 && lot.Remaining == 0 {
		return qty
	}
	return 0
}

// Count records a stock count: each counted drug is adjusted to the counted
// quantity (reason "นับสต็อก"), in one transaction.
func Count(ctx context.Context, mdb *db.MongoDB, in models.StockCountInput) (models.StockCount, error) {
	if len(in.Items) == 0 {
		return models.StockCount{}, refusal.Invalidf("items is required")
	}
	if len(in.Items) > 1000 {
		return models.StockCount{}, refusal.Invalidf("ไม่เกิน 1,000 รายการต่อรอบตรวจนับ")
	}
	type line struct {
		id      bson.ObjectID
		counted int
		lot     *models.LotTarget
	}
	seen := map[bson.ObjectID]bool{}
	lines := make([]line, 0, len(in.Items))
	for _, it := range in.Items {
		if it.Counted < 0 {
			return models.StockCount{}, refusal.Invalidf("counted must be >= 0")
		}
		oid, err := bson.ObjectIDFromHex(it.DrugID)
		if err != nil {
			return models.StockCount{}, refusal.Invalidf("invalid drug id")
		}
		if seen[oid] {
			return models.StockCount{}, refusal.Invalidf("duplicate drug id")
		}
		seen[oid] = true
		lines = append(lines, line{oid, it.Counted, it.Lot})
	}

	tz := mdb.Timezone(ctx)
	var count models.StockCount
	err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now().In(tz)
		countNo, err := nextStockCountNo(txCtx, mdb, now)
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		items := make([]models.StockCountItem, 0, len(lines))
		for _, l := range lines {
			drug, err := loadDrug(txCtx, mdb, l.id)
			if err != nil {
				return err
			}
			delta := l.counted - drug.Stock
			items = append(items, models.StockCountItem{
				DrugID: l.id, DrugName: drug.Name, Unit: drug.Unit, SystemStock: drug.Stock, Counted: l.counted, Delta: delta,
			})
			if delta == 0 {
				continue
			}
			if _, err := adjust(txCtx, mdb, tz, l.id, delta, l.lot, models.AdjustmentReasons[0],
				strings.TrimSpace(countNo+" "+note), now); err != nil {
				return err
			}
		}
		count = models.StockCount{CountNo: countNo, Note: note, Items: items, CreatedAt: now}
		res, err := mdb.StockCounts().InsertOne(txCtx, count)
		if err != nil {
			return err
		}
		count.ID = res.InsertedID.(bson.ObjectID)
		return nil
	})
	return count, err
}

func nextStockCountNo(txCtx context.Context, mdb *db.MongoDB, now time.Time) (string, error) {
	today := now.Format("060102")
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := mdb.Counters().FindOneAndUpdate(txCtx,
		bson.M{"_id": "SC-" + today},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return "", fmt.Errorf("stock count number error: %w", err)
	}
	return fmt.Sprintf("SC-%s-%03d", today, counter.Seq), nil
}

// WriteOffError names the lot that stopped a write-off.
type WriteOffError struct {
	LotID string
	Err   error
}

func (e *WriteOffError) Error() string { return e.Err.Error() }
func (e *WriteOffError) Unwrap() error { return e.Err }

// WriteOff writes off every listed lot, or none: the remaining units leave
// stock, the lot is kept at zero and marked written off, and each write-off
// is recorded.
func WriteOff(ctx context.Context, mdb *db.MongoDB, lotIDs []string) (int, error) {
	if len(lotIDs) == 0 {
		return 0, refusal.Invalidf("lot_ids required")
	}
	ids := make([]bson.ObjectID, len(lotIDs))
	for i, raw := range lotIDs {
		oid, err := bson.ObjectIDFromHex(raw)
		if err != nil {
			return 0, &WriteOffError{LotID: raw, Err: refusal.Invalidf("invalid lot id")}
		}
		ids[i] = oid
	}
	err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		for i, id := range ids {
			var lot models.DrugLot
			err := mdb.DrugLots().FindOne(txCtx, bson.M{"_id": id}).Decode(&lot)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return &WriteOffError{LotID: lotIDs[i], Err: refusal.NotFoundf("lot not found")}
			}
			if err != nil {
				return err
			}
			if lot.WrittenOffAt != nil && lot.Remaining == 0 {
				return &WriteOffError{LotID: lotIDs[i], Err: refusal.Conflictf("lot already written off")}
			}
			if _, err := mdb.DrugLots().UpdateOne(txCtx, bson.M{"_id": id},
				bson.M{"$set": bson.M{"remaining": 0, "written_off_at": now}}); err != nil {
				return err
			}
			if lot.Remaining > 0 {
				if _, err := mdb.Drugs().UpdateOne(txCtx, bson.M{"_id": lot.DrugID}, bson.M{"$inc": bson.M{"stock": -lot.Remaining}}); err != nil {
					return err
				}
			}
			if _, err := mdb.LotWriteoffs().InsertOne(txCtx, models.LotWriteoff{
				DrugID: lot.DrugID, DrugName: lot.DrugName, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate,
				Qty: lot.Remaining, CreatedAt: now, LotID: lot.ID, Reason: "writeoff", By: actor(txCtx),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// DeleteLot removes a lot entered by mistake. A lot any sale has taken from
// must be written off instead, so voids and returns can still give goods
// back to it and its history stays. The removal is recorded.
func DeleteLot(ctx context.Context, mdb *db.MongoDB, drugID, lotID string) error {
	drugOID, err := bson.ObjectIDFromHex(drugID)
	if err != nil {
		return refusal.Invalidf("invalid drug id")
	}
	lotOID, err := bson.ObjectIDFromHex(lotID)
	if err != nil {
		return refusal.Invalidf("invalid lot id")
	}
	return mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		var lot models.DrugLot
		err := mdb.DrugLots().FindOne(txCtx, bson.M{"_id": lotOID, "drug_id": drugOID}).Decode(&lot)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return refusal.NotFoundf("lot not found")
		}
		if err != nil {
			return err
		}
		sold, err := mdb.SaleItems().CountDocuments(txCtx, bson.M{"lot_splits.lot_id": lotOID}, options.Count().SetLimit(1))
		if err != nil {
			return err
		}
		if sold > 0 {
			return refusal.Conflictf("lot has been sold from; write it off instead")
		}
		if _, err := mdb.DrugLots().DeleteOne(txCtx, bson.M{"_id": lotOID}); err != nil {
			return err
		}
		if lot.Remaining > 0 {
			if _, err := mdb.Drugs().UpdateOne(txCtx, bson.M{"_id": drugOID}, bson.M{"$inc": bson.M{"stock": -lot.Remaining}}); err != nil {
				return err
			}
		}
		_, err = mdb.LotWriteoffs().InsertOne(txCtx, models.LotWriteoff{
			DrugID: lot.DrugID, DrugName: lot.DrugName, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate,
			Qty: lot.Remaining, CreatedAt: time.Now(), LotID: lot.ID, Reason: deletedLot, By: actor(txCtx),
			Received: receivedBy(lot),
		})
		return err
	})
}

// DriftRow is a lot-tracked drug whose stock differs from its lots less its
// unsettled oversold quantity.
type DriftRow struct {
	DrugID       bson.ObjectID `json:"drug_id"`
	DrugName     string        `json:"drug_name"`
	Stock        int           `json:"stock"`
	LotRemaining int           `json:"lot_remaining"`
	Oversold     int           `json:"oversold"`
	// Difference = stock − (lot_remaining − oversold); zero when consistent.
	Difference int `json:"difference"`
}

// Drift lists lot-tracked drugs whose stock and lots disagree, so the
// pharmacy can decide how to correct them (ADR-0007). It changes nothing.
func Drift(ctx context.Context, mdb *db.MongoDB) ([]DriftRow, error) {
	sums := func(c *mongo.Collection, match bson.M, field string) (map[bson.ObjectID]int, error) {
		cur, err := c.Aggregate(ctx, bson.A{
			bson.M{"$match": match},
			bson.M{"$group": bson.M{"_id": "$drug_id", "total": bson.M{"$sum": "$" + field}}},
		})
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID    bson.ObjectID `bson:"_id"`
			Total int           `bson:"total"`
		}
		if err := cur.All(ctx, &rows); err != nil {
			return nil, err
		}
		out := make(map[bson.ObjectID]int, len(rows))
		for _, r := range rows {
			out[r.ID] = r.Total
		}
		return out, nil
	}
	lots, err := sums(mdb.DrugLots(), bson.M{}, "remaining")
	if err != nil {
		return nil, err
	}
	oversold, err := sums(mdb.SaleItems(), bson.M{"oversold_qty": bson.M{"$gt": 0}}, "oversold_qty")
	if err != nil {
		return nil, err
	}
	ids := make([]bson.ObjectID, 0, len(lots))
	for id := range lots {
		ids = append(ids, id)
	}
	cur, err := mdb.Drugs().Find(ctx, bson.M{"_id": bson.M{"$in": ids}}, options.Find().SetProjection(bson.M{"name": 1, "stock": 1}))
	if err != nil {
		return nil, err
	}
	var drugs []models.Drug
	if err := cur.All(ctx, &drugs); err != nil {
		return nil, err
	}
	rows := []DriftRow{}
	for _, d := range drugs {
		diff := d.Stock - (lots[d.ID] - oversold[d.ID])
		if diff != 0 {
			rows = append(rows, DriftRow{DrugID: d.ID, DrugName: d.Name, Stock: d.Stock, LotRemaining: lots[d.ID], Oversold: oversold[d.ID], Difference: diff})
		}
	}
	slices.SortFunc(rows, func(a, b DriftRow) int { return strings.Compare(a.DrugName, b.DrugName) })
	return rows, nil
}

const deletedLot = "deleted"

// receivedBy is what the lot's own receipt put into stock: nothing for a lot
// a stock increase created, whose adjustment is its movement.
func receivedBy(lot models.DrugLot) *int {
	received := lot.Quantity
	if lot.Origin == models.LotAdjustment {
		received = 0
	}
	return &received
}
