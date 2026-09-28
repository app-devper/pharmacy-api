// Package receiving owns goods receipt (ADR-0012): a purchase order is drafted
// and revised, then confirmed once, which receives each line as a lot through
// inventory and records its ขย.9 row, in one transaction. One rule checks a
// line at every step; a draft may leave the lot number or expiry for later,
// but confirming needs them on every line. Drug name, registration number
// and unit always come from the catalog.
package receiving

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
	"pharmacy-pos/backend/refusal"
)

const (
	dayLayout = "2006-01-02"
	draft     = "draft"
	confirmed = "confirmed"
)

// Draft records a new purchase order as a draft with the next IMP-YYMMDD-NNN
// number of the pharmacy's day.
func Draft(ctx context.Context, mdb *db.MongoDB, in models.POInput) (models.PurchaseOrder, error) {
	tz := mdb.Timezone(ctx)
	now := time.Now()
	receiveDate, err := receiveDateOf(in.ReceiveDate, now, tz)
	if err != nil {
		return models.PurchaseOrder{}, err
	}
	items, total, err := lines(ctx, mdb, in.Items, tz)
	if err != nil {
		return models.PurchaseOrder{}, err
	}
	docNo, err := nextDocNo(ctx, mdb, now.In(tz))
	if err != nil {
		return models.PurchaseOrder{}, err
	}
	po := models.PurchaseOrder{
		DocNo: docNo, Supplier: in.Supplier, InvoiceNo: in.InvoiceNo, ReceiveDate: receiveDate,
		Items: items, ItemCount: len(items), TotalCost: total, Status: draft, Notes: in.Notes, CreatedAt: now,
	}
	res, err := mdb.PurchaseOrders().InsertOne(ctx, po)
	if err != nil {
		return models.PurchaseOrder{}, err
	}
	po.ID = res.InsertedID.(bson.ObjectID)
	return po, nil
}

// Revise replaces a draft's content.
func Revise(ctx context.Context, mdb *db.MongoDB, id string, in models.POInput) (models.PurchaseOrder, error) {
	po, err := load(ctx, mdb, id)
	if err != nil {
		return po, err
	}
	if po.Status != draft {
		return po, refusal.Conflictf("cannot edit a confirmed order")
	}
	tz := mdb.Timezone(ctx)
	receiveDate, err := receiveDateOf(in.ReceiveDate, po.ReceiveDate, tz)
	if err != nil {
		return po, err
	}
	items, total, err := lines(ctx, mdb, in.Items, tz)
	if err != nil {
		return po, err
	}
	res, err := mdb.PurchaseOrders().UpdateOne(ctx, bson.M{"_id": po.ID, "status": draft}, bson.M{"$set": bson.M{
		"supplier": in.Supplier, "invoice_no": in.InvoiceNo, "receive_date": receiveDate, "notes": in.Notes,
		"items": items, "item_count": len(items), "total_cost": total,
	}})
	if err != nil {
		return po, err
	}
	if res.MatchedCount == 0 {
		return po, refusal.Conflictf("cannot edit a confirmed order")
	}
	po.Supplier, po.InvoiceNo, po.ReceiveDate, po.Notes = in.Supplier, in.InvoiceNo, receiveDate, in.Notes
	po.Items, po.ItemCount, po.TotalCost = items, len(items), total
	return po, nil
}

// Discard deletes a draft.
func Discard(ctx context.Context, mdb *db.MongoDB, id string) error {
	po, err := load(ctx, mdb, id)
	if err != nil {
		return err
	}
	if po.Status != draft {
		return refusal.Conflictf("cannot delete a confirmed order")
	}
	res, err := mdb.PurchaseOrders().DeleteOne(ctx, bson.M{"_id": po.ID, "status": draft})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return refusal.Conflictf("cannot delete a confirmed order")
	}
	return nil
}

// Confirm receives every line of a draft as a lot and records its ขย.9 row,
// then marks the order confirmed, all in one transaction. A line without a
// lot number or expiry refuses the whole order, naming every such line.
func Confirm(ctx context.Context, mdb *db.MongoDB, id string) (models.PurchaseOrder, error) {
	po, err := load(ctx, mdb, id)
	if err != nil {
		return po, err
	}
	if po.Status != draft {
		return po, refusal.Conflictf("purchase order is already confirmed")
	}
	if len(po.Items) == 0 {
		return po, refusal.Invalidf("no items to confirm")
	}
	tz := mdb.Timezone(ctx)
	var problems []string
	for i, item := range po.Items {
		if strings.TrimSpace(item.LotNumber) == "" {
			problems = append(problems, fmt.Sprintf("รายการที่ %d: กรุณาระบุล็อตหมายเลข", i+1))
		}
		if strings.TrimSpace(item.ExpiryDate) == "" {
			problems = append(problems, fmt.Sprintf("รายการที่ %d: กรุณาระบุวันหมดอายุ", i+1))
		} else if _, err := time.ParseInLocation(dayLayout, item.ExpiryDate, tz); err != nil {
			problems = append(problems, fmt.Sprintf("รายการที่ %d: วันหมดอายุไม่ถูกต้อง", i+1))
		}
		if item.Qty <= 0 {
			problems = append(problems, fmt.Sprintf("รายการที่ %d: จำนวนต้องมากกว่า 0", i+1))
		}
	}
	if len(problems) > 0 {
		return po, refusal.Invalidf(strings.Join(problems, "\n"))
	}

	receiveDay := po.ReceiveDate.In(tz).Format(dayLayout)
	now := time.Now()
	err = mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		for _, item := range po.Items {
			var drug models.Drug
			err := mdb.Drugs().FindOne(txCtx, bson.M{"_id": item.DrugID}).Decode(&drug)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return refusal.Invalidf("drug not found: " + item.DrugName)
			}
			if err != nil {
				return err
			}
			expiry, _ := time.ParseInLocation(dayLayout, item.ExpiryDate, tz)
			cost := item.CostPrice
			if _, err := inventory.ReceiveLot(txCtx, mdb, models.DrugLot{
				DrugID: drug.ID, DrugName: drug.Name, LotNumber: strings.TrimSpace(item.LotNumber), ExpiryDate: expiry,
				ImportDate: po.ReceiveDate, CostPrice: &cost, SellPrice: item.SellPrice, Quantity: item.Qty, CreatedAt: now,
			}); err != nil {
				return err
			}
			ky9 := models.Ky9Input{
				Date: receiveDay, DrugName: drug.Name, RegNo: drug.RegNo, Unit: drug.Unit, Qty: item.Qty,
				PricePerUnit: item.CostPrice, Seller: po.Supplier, InvoiceNo: po.InvoiceNo,
			}.Record(now)
			if _, err := mdb.Ky9().InsertOne(txCtx, ky9); err != nil {
				return err
			}
		}
		res, err := mdb.PurchaseOrders().UpdateOne(txCtx, bson.M{"_id": po.ID, "status": draft},
			bson.M{"$set": bson.M{"status": confirmed, "confirmed_at": now}})
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return refusal.Conflictf("purchase order is already confirmed")
		}
		return nil
	})
	if err != nil {
		return po, err
	}
	po.Status, po.ConfirmedAt = confirmed, &now
	return po, nil
}

func load(ctx context.Context, mdb *db.MongoDB, id string) (models.PurchaseOrder, error) {
	var po models.PurchaseOrder
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return po, refusal.Invalidf("invalid id")
	}
	err = mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return po, refusal.NotFoundf("not found")
	}
	return po, err
}

func receiveDateOf(raw string, fallback time.Time, tz *time.Location) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.ParseInLocation(dayLayout, raw, tz)
	if err != nil {
		return time.Time{}, refusal.Invalidf("receive_date must be YYYY-MM-DD")
	}
	return t, nil
}

// lines checks every line of a draft and names its drug from the catalog.
// What is given must be valid; lot number and expiry may still be empty.
func lines(ctx context.Context, mdb *db.MongoDB, inputs []models.POItemInput, tz *time.Location) ([]models.POItem, float64, error) {
	if len(inputs) == 0 {
		return nil, 0, refusal.Invalidf("items is required")
	}
	items := make([]models.POItem, 0, len(inputs))
	total := 0.0
	for i, in := range inputs {
		n := i + 1
		switch {
		case in.Qty <= 0:
			return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: จำนวนต้องมากกว่า 0", n))
		case in.CostPrice < 0:
			return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: ราคาทุนต้องไม่ติดลบ", n))
		case in.SellPrice != nil && *in.SellPrice < 0:
			return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: ราคาขายต้องไม่ติดลบ", n))
		}
		expiry := strings.TrimSpace(in.ExpiryDate)
		if expiry != "" {
			if _, err := time.ParseInLocation(dayLayout, expiry, tz); err != nil {
				return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: วันหมดอายุไม่ถูกต้อง", n))
			}
		}
		oid, err := bson.ObjectIDFromHex(in.DrugID)
		if err != nil {
			return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: ไม่พบยา", n))
		}
		var drug models.Drug
		err = mdb.Drugs().FindOne(ctx, bson.M{"_id": oid}).Decode(&drug)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, 0, refusal.Invalidf(fmt.Sprintf("รายการที่ %d: ไม่พบยา", n))
		}
		if err != nil {
			return nil, 0, err
		}
		sell := in.SellPrice
		if sell == nil {
			sell = &drug.SellPrice
		}
		// in.DrugName is accepted from older clients but never used.
		items = append(items, models.POItem{
			DrugID: oid, DrugName: drug.Name, LotNumber: strings.TrimSpace(in.LotNumber), ExpiryDate: expiry,
			Qty: in.Qty, CostPrice: in.CostPrice, SellPrice: sell,
		})
		total += float64(in.Qty) * in.CostPrice
	}
	return items, total, nil
}

func nextDocNo(ctx context.Context, mdb *db.MongoDB, localNow time.Time) (string, error) {
	day := localNow.Format("060102")
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": "IMP-" + day},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return "", fmt.Errorf("doc_no generation failed: %w", err)
	}
	return fmt.Sprintf("IMP-%s-%03d", day, counter.Seq), nil
}
