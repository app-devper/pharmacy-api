package sales

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	"pharmacy-pos/backend/models"
)

// Return records a Return against a confirmed Sale: goods go back to drug
// stock and to the lots they came from, and the customer's spend drops by
// the refund, in one transaction. A return may never exceed what was sold.
// With a client_request_id it is a Commercial command: repeating it returns
// the recorded Return (replayed) instead of returning goods again.
func Return(ctx context.Context, mdb *db.MongoDB, saleID string, input models.DrugReturnInput) (out models.DrugReturn, replayed bool, err error) {
	oid, err := bson.ObjectIDFromHex(saleID)
	if err != nil {
		return out, false, invalid("invalid id")
	}
	if input.Reason == "" {
		return out, false, invalid("reason is required")
	}
	if len(input.Items) == 0 {
		return out, false, invalid("items is required")
	}
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	fp := fingerprint(struct {
		SaleID string                   `json:"sale_id"`
		Items  []models.ReturnItemInput `json:"items"`
		Reason string                   `json:"reason"`
	}{saleID, input.Items, input.Reason})

	find := func(ctx context.Context) (models.DrugReturn, string, error) {
		var ret models.DrugReturn
		err := mdb.DrugReturns().FindOne(ctx, bson.M{"client_request_id": input.ClientRequestID}).Decode(&ret)
		return ret, ret.RequestFingerprint, err
	}
	return runOnce(ctx, input.ClientRequestID, fp, find, func() (models.DrugReturn, error) {
		requestFp := ""
		if input.ClientRequestID != "" {
			requestFp = fp
		}
		return recordReturn(ctx, mdb, oid, input, requestFp)
	})
}

func recordReturn(ctx context.Context, mdb *db.MongoDB, oid bson.ObjectID, input models.DrugReturnInput, requestFp string) (models.DrugReturn, error) {
	tz := mdb.Timezone(ctx)

	var sale models.Sale
	if err := mdb.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		return models.DrugReturn{}, notFound("sale not found")
	}
	if sale.Voided {
		return models.DrugReturn{}, invalid("ไม่สามารถคืนยาจากบิลที่ยกเลิกแล้ว")
	}

	itemCur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return models.DrugReturn{}, err
	}
	defer itemCur.Close(ctx)
	var saleItems []models.SaleItem
	if err := itemCur.All(ctx, &saleItems); err != nil {
		return models.DrugReturn{}, err
	}

	saleItemMap := make(map[string]models.SaleItem, len(saleItems))
	for _, si := range saleItems {
		saleItemMap[si.ID.Hex()] = si
	}

	for _, inp := range input.Items {
		if _, ok := saleItemMap[inp.SaleItemID]; !ok {
			return models.DrugReturn{}, invalid(fmt.Sprintf("sale item %s not found", inp.SaleItemID))
		}
		if inp.Qty <= 0 {
			return models.DrugReturn{}, invalid("qty must be > 0")
		}
	}

	var ret models.DrugReturn
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		currentReturned := make(map[string]int)
		retCur, err := mdb.DrugReturns().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return fmt.Errorf("failed to load existing returns: %w", err)
		}
		var txReturns []models.DrugReturn
		if err := retCur.All(txCtx, &txReturns); err != nil {
			retCur.Close(txCtx)
			return err
		}
		retCur.Close(txCtx)
		for _, ret := range txReturns {
			for _, ri := range ret.Items {
				currentReturned[ri.SaleItemID.Hex()] += ri.Qty
			}
		}

		requested := map[string]int{}
		for _, inp := range input.Items {
			requested[inp.SaleItemID] += inp.Qty
		}
		for id, qty := range requested {
			si := saleItemMap[id]
			line := Returnable(si, currentReturned[id])
			if qty <= line.Returnable {
				continue
			}
			if line.Unlinked > 0 && qty <= si.Qty-line.Returned {
				return invalid(fmt.Sprintf("ยา %s: คืนได้สูงสุด %d (มี %d หน่วยยังไม่ผูกกับล็อตจริง)", si.DrugName, line.Returnable, line.Unlinked))
			}
			return invalid(fmt.Sprintf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, line.Returned))
		}

		now := time.Now()
		// Counter keyed by local calendar day so same-day returns share one seq
		// and the RET-YYMMDD prefix matches the pharmacy's local date.
		today := now.In(tz).Format("060102")
		counterID := "RET-" + today
		var counter struct {
			Seq int `bson:"seq"`
		}
		if err := mdb.Counters().FindOneAndUpdate(txCtx,
			bson.M{"_id": counterID},
			bson.M{"$inc": bson.M{"seq": 1}},
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
		).Decode(&counter); err != nil {
			return fmt.Errorf("return number error: %w", err)
		}
		returnNo := fmt.Sprintf("RET-%s-%03d", today, counter.Seq)

		returnItems := make([]models.ReturnItem, 0, len(input.Items))
		refund := 0.0

		for _, inp := range input.Items {
			si := saleItemMap[inp.SaleItemID]
			siOID, _ := bson.ObjectIDFromHex(inp.SaleItemID)

			// The customer paid each line's share of the bill after its bill
			// discount; a return refunds that share (ADR-0008).
			subtotal := float64(inp.Qty) * si.Price * paidShare(sale)
			costSubtotal := 0.0
			if si.Qty > 0 {
				costSubtotal = (si.CostSubtotal / float64(si.Qty)) * float64(inp.Qty)
			}
			refund += subtotal

			returnItems = append(returnItems, models.ReturnItem{
				SaleItemID:   siOID,
				DrugID:       si.DrugID,
				DrugName:     si.DrugName,
				Qty:          inp.Qty,
				Price:        si.Price,
				Subtotal:     subtotal,
				CostSubtotal: costSubtotal,
			})

			if err := inventory.GiveBack(txCtx, mdb, si, inp.Qty, currentReturned[inp.SaleItemID]); err != nil {
				return err
			}
		}

		ret = models.DrugReturn{
			ReturnNo:     returnNo,
			SaleID:       oid,
			BillNo:       sale.BillNo,
			CustomerID:   sale.CustomerID,
			CustomerName: sale.CustomerName,
			Items:        returnItems,
			Refund:       refund,
			Reason:       input.Reason,
			ReturnedAt:   now,

			ClientRequestID:    input.ClientRequestID,
			RequestFingerprint: requestFp,
		}
		res, err := mdb.DrugReturns().InsertOne(txCtx, ret)
		if err != nil {
			return err
		}
		ret.ID = res.InsertedID.(bson.ObjectID)

		if sale.CustomerID != nil {
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": sale.CustomerID},
				bson.M{"$inc": bson.M{"total_spent": -refund}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}

		return recordDayEffect(txCtx, mdb, models.EodAdjustment{
			Date: businessDay(now, tz), Kind: models.AdjustReturn, RefID: ret.ID, RefNo: returnNo,
			SalesDelta: -refund, CashDelta: -refund,
		})
	}); err != nil {
		if db.IsDuplicateKey(err) {
			return models.DrugReturn{}, err // runOnce replays the committed attempt
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.DrugReturn{}, invalid("referenced document not found")
		}
		return models.DrugReturn{}, err
	}
	return ret, nil
}

// paidShare is the fraction of each line's subtotal the customer paid once the
// bill discount is spread over the bill's lines.
func paidShare(sale models.Sale) float64 {
	if gross := sale.Total + sale.Discount; gross > 0 {
		return sale.Total / gross
	}
	return 1
}

// LineReturn is how much of a sale line has been and can still be returned,
// in base units. Only units sold from a real lot can go back (ADR-0007):
// Unlinked units (oversold, or reconciled from bulk stock) cannot.
type LineReturn struct {
	Returned   int
	Returnable int
	Unlinked   int
}

// Returnable applies the return rule to a sale line with returned units
// already returned. Return enforces it and the sale's items report it. A
// line of a drug without lots (no splits, nothing oversold) returns to stock
// alone.
func Returnable(item models.SaleItem, returned int) LineReturn {
	unlinked := item.OversoldQty
	for _, sp := range item.LotSplits {
		if sp.LotID.IsZero() {
			unlinked += sp.Qty
		}
	}
	unlinked = min(unlinked, item.Qty)
	return LineReturn{Returned: returned, Returnable: max(item.Qty-unlinked-returned, 0), Unlinked: unlinked}
}

// Lines lists a sale's items with what each can still return.
func Lines(ctx context.Context, mdb *db.MongoDB, saleID string) ([]models.SaleLine, error) {
	oid, err := bson.ObjectIDFromHex(saleID)
	if err != nil {
		return nil, invalid("invalid id")
	}
	var items []models.SaleItem
	cur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return nil, err
	}
	if err := cur.All(ctx, &items); err != nil {
		return nil, err
	}
	var returns []models.DrugReturn
	cur, err = mdb.DrugReturns().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return nil, err
	}
	if err := cur.All(ctx, &returns); err != nil {
		return nil, err
	}
	returned := map[bson.ObjectID]int{}
	for _, r := range returns {
		for _, ri := range r.Items {
			returned[ri.SaleItemID] += ri.Qty
		}
	}
	out := make([]models.SaleLine, 0, len(items))
	for _, it := range items {
		l := Returnable(it, returned[it.ID])
		out = append(out, models.SaleLine{SaleItem: it, ReturnedQty: l.Returned, ReturnableQty: l.Returnable, UnlinkedQty: l.Unlinked})
	}
	return out, nil
}
