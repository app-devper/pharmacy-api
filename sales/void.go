package sales

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
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
	tz := mdb.Timezone(ctx)

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

			// Goods come back to the lots they were taken from (inventory).
			if err := inventory.GiveBack(txCtx, mdb, item, restoreQty, returnedByItem[item.ID]); err != nil {
				return err
			}
		}

		// A bill already refunded in full leaves the customer's spend as is.
		if remainingSpend := sale.Total - refunded; sale.CustomerID != nil && remainingSpend > 0 {
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

		return recordDayEffect(txCtx, mdb, models.EodAdjustment{
			Date: businessDay(sale.SoldAt, tz), Kind: models.AdjustVoid, RefID: oid, RefNo: sale.BillNo,
			BillDelta: -1, SalesDelta: -sale.Total, CashDelta: -(sale.Received - sale.Change),
		})
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
