package sales

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Void cancels a whole Sale: stock not already returned goes back to drug
// stock and to the lots it came from, and the customer's spend drops by what
// was not already refunded. Voiding twice is a Conflict ("sale already
// voided"), so a repeated void cannot reverse stock twice.
func Void(ctx context.Context, mdb *db.MongoDB, saleID, reason string) error {
	oid, err := bson.ObjectIDFromHex(saleID)
	if err != nil {
		return invalid("invalid id")
	}
	var sale models.Sale
	if err := mdb.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		return notFound("sale not found")
	}
	if sale.Voided {
		return conflict(errAlreadyVoided.Error())
	}

	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		updateRes, err := mdb.Sales().UpdateOne(txCtx,
			bson.M{"_id": oid, "voided": bson.M{"$ne": true}},
			bson.M{"$set": bson.M{
				"voided":      true,
				"void_reason": reason,
				"voided_at":   now,
			}},
		)
		if err != nil {
			return err
		}
		if updateRes.MatchedCount == 0 {
			return errAlreadyVoided
		}

		itemCur, err := mdb.SaleItems().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return err
		}
		defer itemCur.Close(txCtx)

		var items []models.SaleItem
		if err := itemCur.All(txCtx, &items); err != nil {
			return err
		}

		retCur, err := mdb.DrugReturns().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return err
		}
		defer retCur.Close(txCtx)

		var returns []models.DrugReturn
		if err := retCur.All(txCtx, &returns); err != nil {
			return err
		}

		returnedByItem := make(map[bson.ObjectID]int, len(items))
		refunded := 0.0
		for _, ret := range returns {
			refunded += ret.Refund
			for _, item := range ret.Items {
				returnedByItem[item.SaleItemID] += item.Qty
			}
		}

		for _, item := range items {
			restoreQty := item.Qty - returnedByItem[item.ID]
			if restoreQty <= 0 {
				continue
			}

			if _, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": item.DrugID},
				bson.M{"$inc": bson.M{"stock": restoreQty}},
			); err != nil {
				return err
			}

			// Only restore to real lots the portion of the sale that was
			// actually deducted from a lot. LotSplits tell the truth:
			//  • Real splits (non-zero LotID) — from lots at sale or import
			//    reconcile → reverse back to those lots.
			//  • Synthetic splits (LotID == zero) — from stock adjustments
			//    → no lot to give back to; drug.stock was already credited
			//    above, nothing else to do.
			//  • Unreconciled OversoldQty — no lot ever assigned; also just
			//    forgiven via the stock credit.
			// Prior returns are assumed to have eaten the real-lot portion
			// first (worst case), so we subtract returned qty from it too.
			lotCovered := 0
			for _, sp := range item.LotSplits {
				if !sp.LotID.IsZero() {
					lotCovered += sp.Qty
				}
			}
			lotCovered -= returnedByItem[item.ID]
			if lotCovered > restoreQty {
				lotCovered = restoreQty
			}
			if lotCovered > 0 {
				if err := restoreSaleItemLots(txCtx, mdb, item, lotCovered, returnedByItem[item.ID]); err != nil {
					return err
				}
			}
		}

		if sale.CustomerID != nil {
			remainingSpend := sale.Total - refunded
			if remainingSpend <= 0 {
				return nil
			}
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": sale.CustomerID},
				bson.M{"$inc": bson.M{"total_spent": -remainingSpend}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}

		return nil
	}); err != nil {
		if errors.Is(err, errAlreadyVoided) {
			return conflict(err.Error())
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			return invalid("referenced document not found")
		}
		return err
	}
	return nil
}
