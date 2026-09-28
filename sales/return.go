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

	retCur, err := mdb.DrugReturns().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		return models.DrugReturn{}, fmt.Errorf("failed to load existing returns: %w", err)
	}
	defer retCur.Close(ctx)
	var existingReturns []models.DrugReturn
	if err := retCur.All(ctx, &existingReturns); err != nil {
		return models.DrugReturn{}, err
	}

	alreadyReturned := make(map[string]int)
	for _, ret := range existingReturns {
		for _, ri := range ret.Items {
			alreadyReturned[ri.SaleItemID.Hex()] += ri.Qty
		}
	}

	for _, inp := range input.Items {
		si, ok := saleItemMap[inp.SaleItemID]
		if !ok {
			return models.DrugReturn{}, invalid(fmt.Sprintf("sale item %s not found", inp.SaleItemID))
		}
		if inp.Qty <= 0 {
			return models.DrugReturn{}, invalid("qty must be > 0")
		}
		if inp.Qty+alreadyReturned[inp.SaleItemID] > si.Qty {
			return models.DrugReturn{}, invalid(fmt.Sprintf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, alreadyReturned[inp.SaleItemID]))
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

		for _, inp := range input.Items {
			si := saleItemMap[inp.SaleItemID]
			if inp.Qty+currentReturned[inp.SaleItemID] > si.Qty {
				return fmt.Errorf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, currentReturned[inp.SaleItemID])
			}
			// Only units backed by a real lot can be returned — the unreconciled
			// oversold portion has no lot to give back to, and the synthetic
			// (adjustment-reconciled) portion was absorbed from bulk stock
			// rather than a specific lot. Cap returnable at the sum of real
			// LotSplits (non-zero LotID).
			realLotQty := 0
			for _, sp := range si.LotSplits {
				if !sp.LotID.IsZero() {
					realLotQty += sp.Qty
				}
			}
			if realLotQty < si.Qty {
				if inp.Qty+currentReturned[inp.SaleItemID] > realLotQty {
					pending := si.Qty - realLotQty
					return fmt.Errorf("ยา %s: คืนได้สูงสุด %d (มี %d หน่วยยังไม่ผูกกับล็อตจริง)", si.DrugName, realLotQty, pending)
				}
			}
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
