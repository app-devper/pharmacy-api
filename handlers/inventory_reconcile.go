package handlers

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// reconcileOversoldFromAdjustment drains up to `available` base units from
// pending oversold SaleItems when the admin bumps stock via a manual
// adjustment (not an import). Returns the total qty actually drained so the
// caller can offset drug.stock accordingly.
//
// Unlike reconcileOversold — which absorbs into a real DrugLot and records
// the lot_id/expiry on lot_splits — this path has no lot to attribute to.
// We still append a synthetic LotDeduction to lot_splits so the audit trail
// is complete; it uses a zero LotID and a LotNumber of "ADJUST:<reason>" so
// pharmacists can tell at a glance that this portion was reconciled by an
// adjustment rather than an import. Callers MUST already be inside a
// transaction so drug.stock and oversold_qty stay in sync on failure.
func reconcileOversoldFromAdjustment(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, available int, reason string) (int, error) {
	if available <= 0 {
		return 0, nil
	}
	cur, err := mdb.SaleItems().Find(ctx,
		bson.M{"drug_id": drugID, "oversold_qty": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		return 0, err
	}

	remaining := available
	drained := 0
	marker := "ADJUST"
	if reason != "" {
		marker = "ADJUST:" + reason
	}
	now := time.Now()
	for _, si := range items {
		if remaining <= 0 {
			break
		}
		take := si.OversoldQty
		if take > remaining {
			take = remaining
		}
		synthetic := models.LotDeduction{
			LotID:      bson.NilObjectID,
			LotNumber:  marker,
			ExpiryDate: now, // no real expiry; use now to keep field non-zero
			Qty:        take,
		}
		if _, err := mdb.SaleItems().UpdateOne(ctx,
			bson.M{"_id": si.ID},
			bson.M{
				"$inc":  bson.M{"oversold_qty": -take},
				"$push": bson.M{"lot_splits": synthetic},
			},
		); err != nil {
			return drained, err
		}
		remaining -= take
		drained += take
	}
	return drained, nil
}

// reconcileOversold drains `lot`'s remaining against older SaleItems that were
// sold on credit (AllowOversell=true) and never got a matching lot deduction.
//
// Processed in sold_at ASC order — the oldest debt gets paid first. For each
// eligible SaleItem the function:
//  1. Chooses `drain = min(lot.remaining, si.oversold_qty)`.
//  2. Decrements the lot via `$inc remaining -drain` with a `$gte` guard.
//  3. Decrements `oversold_qty` on the SaleItem and appends a LotDeduction
//     to `lot_splits` so the audit trail for that sale becomes complete.
//
// drug.stock is deliberately NOT touched — the oversold sale already debited
// it at sale time, and the caller just credited it with the full lot.Qty.
// Stops early when the lot is empty.
//
// Callers must already be inside a transaction so a partial drain doesn't leak.
func reconcileOversold(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, lot models.DrugLot) error {
	remaining := lot.Remaining
	if remaining <= 0 {
		return nil
	}
	cur, err := mdb.SaleItems().Find(ctx,
		bson.M{"drug_id": drugID, "oversold_qty": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), // ObjectID ≈ insertion order ≈ sold_at ASC
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		return err
	}

	for _, si := range items {
		if remaining <= 0 {
			break
		}
		drain := si.OversoldQty
		if drain > remaining {
			drain = remaining
		}

		// Decrement the lot with $gte guard — protects against a concurrent
		// reconcile draining it first.
		lotRes, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": drain}},
			bson.M{"$inc": bson.M{"remaining": -drain}},
		)
		if err != nil {
			return err
		}
		if lotRes.MatchedCount == 0 {
			// Someone else drained it; stop — the outer reconcile for the
			// next lot (if any) will pick up the slack.
			break
		}

		// Append to this SaleItem's audit trail and reduce its debt.
		split := models.LotDeduction{
			LotID:      lot.ID,
			LotNumber:  lot.LotNumber,
			ExpiryDate: lot.ExpiryDate,
			Qty:        drain,
		}
		if _, err := mdb.SaleItems().UpdateOne(ctx,
			bson.M{"_id": si.ID},
			bson.M{
				"$inc":  bson.M{"oversold_qty": -drain},
				"$push": bson.M{"lot_splits": split},
			},
		); err != nil {
			return err
		}
		remaining -= drain
	}
	return nil
}
