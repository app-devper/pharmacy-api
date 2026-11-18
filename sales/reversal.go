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

const msgReturnOfVoidedSale = "ไม่สามารถคืนยาจากบิลที่ยกเลิกแล้ว"

// saleReversal is a Sale as it stands for a reversal: its lines, what each line has already returned, and what was refunded.
type saleReversal struct {
	Sale           models.Sale
	Items          []models.SaleItem
	ReturnedByItem map[bson.ObjectID]int
	Refunded       float64
}

// loadReversal reads a Sale with its lines and Returns. An absent Sale is
// notFound; a voided one is loaded like any other, and the caller decides.
func loadReversal(ctx context.Context, mdb *db.MongoDB, oid bson.ObjectID) (saleReversal, error) {
	var sale models.Sale
	if err := mdb.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return saleReversal{}, notFound("sale not found")
		}
		return saleReversal{}, err
	}
	var items []models.SaleItem
	itemCur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return saleReversal{}, err
	}
	if err := itemCur.All(ctx, &items); err != nil {
		return saleReversal{}, err
	}
	var returns []models.DrugReturn
	retCur, err := mdb.DrugReturns().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return saleReversal{}, err
	}
	if err := retCur.All(ctx, &returns); err != nil {
		return saleReversal{}, err
	}
	byItem, refunded := aggregateReturns(returns)
	return saleReversal{Sale: sale, Items: items, ReturnedByItem: byItem, Refunded: refunded}, nil
}

func (r saleReversal) returnedOf(itemID bson.ObjectID) int {
	return r.ReturnedByItem[itemID]
}

func (r saleReversal) line(itemID bson.ObjectID) (models.SaleItem, bool) {
	for _, it := range r.Items {
		if it.ID == itemID {
			return it, true
		}
	}
	return models.SaleItem{}, false
}

// aggregateReturns sums what each sale line has had returned and the total refunded.
func aggregateReturns(returns []models.DrugReturn) (byItem map[bson.ObjectID]int, refunded float64) {
	byItem = make(map[bson.ObjectID]int)
	for _, ret := range returns {
		refunded += ret.Refund
		for _, ri := range ret.Items {
			byItem[ri.SaleItemID] += ri.Qty
		}
	}
	return byItem, refunded
}

// claimSale marks the Sale as reversed-now inside txCtx; a Sale already voided is refused with the Return message.
// The write makes a concurrent Void of the same Sale conflict with this
// transaction, so the two serialize instead of both reversing stock.
func claimSale(txCtx context.Context, mdb *db.MongoDB, oid bson.ObjectID, now time.Time) error {
	res, err := mdb.Sales().UpdateOne(txCtx,
		bson.M{"_id": oid, "voided": bson.M{"$ne": true}},
		bson.M{"$set": bson.M{"last_reversal_at": now}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount > 0 {
		return nil
	}
	var sale models.Sale
	if err := mdb.Sales().FindOne(txCtx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return notFound("sale not found")
		}
		return err
	}
	return invalid(msgReturnOfVoidedSale)
}
