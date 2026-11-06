package sales

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

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

	var ret models.DrugReturn
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		if err := claimSale(txCtx, mdb, oid, now); err != nil {
			return err
		}
		r, err := loadReversal(txCtx, mdb, oid)
		if err != nil {
			return err
		}
		sale := r.Sale

		requested := map[bson.ObjectID]int{}
		lines := make([]models.SaleItem, 0, len(input.Items))
		for _, inp := range input.Items {
			id, idErr := bson.ObjectIDFromHex(inp.SaleItemID)
			if idErr != nil {
				return invalid(fmt.Sprintf("sale item %s not found", inp.SaleItemID))
			}
			si, ok := r.line(id)
			if !ok {
				return invalid(fmt.Sprintf("sale item %s not found", inp.SaleItemID))
			}
			if inp.Qty <= 0 {
				return invalid("qty must be > 0")
			}
			requested[id] += inp.Qty
			lines = append(lines, si)
		}
		for id, qty := range requested {
			si, _ := r.line(id)
			line := Returnable(si, r.returnedOf(id))
			if qty <= line.Returnable {
				continue
			}
			if line.Unlinked > 0 && qty <= si.Qty-line.Returned {
				return invalid(fmt.Sprintf("ยา %s: คืนได้สูงสุด %d (มี %d หน่วยยังไม่ผูกกับล็อตจริง)", si.DrugName, line.Returnable, line.Unlinked))
			}
			return invalid(fmt.Sprintf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, line.Returned))
		}

		// Numbered by the pharmacy's business day, like bills.
		returnNo, err := mdb.NextDocNo(txCtx, "RET", now.In(tz))
		if err != nil {
			return err
		}

		returnItems := make([]models.ReturnItem, 0, len(input.Items))
		refund := 0.0

		for i, inp := range input.Items {
			si := lines[i]

			// The customer paid each line's share of the bill after its bill
			// discount; a return refunds that share (ADR-0008).
			subtotal := satang(float64(inp.Qty) * si.Price * paidShare(sale))
			costSubtotal := 0.0
			if si.Qty > 0 {
				costSubtotal = (si.CostSubtotal / float64(si.Qty)) * float64(inp.Qty)
			}
			refund += subtotal

			returnItems = append(returnItems, models.ReturnItem{
				SaleItemID:   si.ID,
				DrugID:       si.DrugID,
				DrugName:     si.DrugName,
				Qty:          inp.Qty,
				Price:        si.Price,
				Subtotal:     subtotal,
				CostSubtotal: costSubtotal,
			})

			if err := inventory.GiveBack(txCtx, mdb, si, inp.Qty, r.returnedOf(si.ID)); err != nil {
				return err
			}
		}

		refund = satang(refund)

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

		if err := applyToCustomer(txCtx, mdb, sale.CustomerID, returnEffect(refund)); err != nil {
			return err
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
	r, err := loadReversal(ctx, mdb, oid)
	if err != nil {
		return nil, err
	}
	out := make([]models.SaleLine, 0, len(r.Items))
	for _, it := range r.Items {
		l := Returnable(it, r.returnedOf(it.ID))
		out = append(out, models.SaleLine{SaleItem: it, ReturnedQty: l.Returned, ReturnableQty: l.Returnable, UnlinkedQty: l.Unlinked})
	}
	return out, nil
}
