// Package inventory owns every change to physical stock (ADR-0007): receiving
// lots, taking and giving back stock for sales, adjustments, stock counts,
// write-offs, and oversold settlement. For a drug that has lots it keeps
//
//	drug.stock = Σ lot.remaining − Σ unsettled oversold quantity
//
// by applying every change to both sides in one transaction, and it always
// writes the audit record. A drug with no lots at all (records from before
// lots existed) keeps stock alone.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/app-devper/um-api/sessionclient"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// sellable is the filter for lots a sale may take from, first expiry first.
func sellable(drugID bson.ObjectID) bson.M {
	return bson.M{"drug_id": drugID, "remaining": bson.M{"$gt": 0}, "written_off_at": bson.M{"$exists": false}}
}

var byExpiry = options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}, {Key: "_id", Value: 1}})

func actor(ctx context.Context) string {
	p, _ := sessionclient.PrincipalFrom(ctx)
	return p.UserID
}

// hasLots reports whether the drug is lot-tracked (has any lot, even empty).
func hasLots(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID) (bool, error) {
	n, err := mdb.DrugLots().CountDocuments(ctx, bson.M{"drug_id": drugID}, options.Count().SetLimit(1))
	return n > 0, err
}

// CheckAvailable refuses a sale of need units the drug's stock and sellable
// lots cannot cover.
func CheckAvailable(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, need int) error {
	var drug models.Drug
	if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
		return err
	}
	if drug.Stock < need {
		return fmt.Errorf("insufficient stock for %s", drug.Name)
	}
	var lots []models.DrugLot
	cur, err := mdb.DrugLots().Find(ctx, sellable(drugID))
	if err != nil {
		return err
	}
	if err := cur.All(ctx, &lots); err != nil {
		return err
	}
	remaining := 0
	for _, l := range lots {
		remaining += l.Remaining
	}
	if len(lots) == 0 {
		// Only a drug with no lots at all sells from stock alone.
		tracked, err := hasLots(ctx, mdb, drugID)
		if err != nil || !tracked {
			return err
		}
	}
	if remaining < need {
		return fmt.Errorf("insufficient lot inventory for %s", drug.Name)
	}
	return nil
}

// Taken is what a sale line took from stock.
type Taken struct {
	Splits       []models.LotDeduction
	CostSubtotal float64
	// Oversold is the part no lot covered, to settle when stock arrives.
	Oversold int
}

// Take removes qty base units of drug for a sale line, first expiry first.
// With allowOversell the shortfall is recorded as oversold instead of
// refused, and stock may go negative. Call inside the sale's transaction.
func Take(txCtx context.Context, mdb *db.MongoDB, drug models.Drug, qty int, allowOversell bool) (Taken, error) {
	filter := bson.M{"_id": drug.ID}
	if !allowOversell {
		filter["stock"] = bson.M{"$gte": qty}
	}
	res, err := mdb.Drugs().UpdateOne(txCtx, filter, bson.M{"$inc": bson.M{"stock": -qty}})
	if err != nil {
		return Taken{}, err
	}
	if res.MatchedCount == 0 {
		return Taken{}, fmt.Errorf("insufficient stock for %s", drug.Name)
	}

	cur, err := mdb.DrugLots().Find(txCtx, sellable(drug.ID), byExpiry)
	if err != nil {
		return Taken{}, err
	}
	var lots []models.DrugLot
	if err := cur.All(txCtx, &lots); err != nil {
		return Taken{}, err
	}
	if len(lots) == 0 {
		tracked, err := hasLots(txCtx, mdb, drug.ID)
		if err != nil {
			return Taken{}, err
		}
		// A drug with no lots at all sells from stock alone; a lot-tracked
		// drug with no sellable lot can only oversell.
		if tracked && !allowOversell {
			return Taken{}, fmt.Errorf("insufficient lot inventory for %s", drug.Name)
		}
		out := Taken{CostSubtotal: float64(qty) * drug.CostPrice}
		if allowOversell {
			out.Oversold = qty
		}
		return out, nil
	}

	need := qty
	out := Taken{}
	for _, lot := range lots {
		if need <= 0 {
			break
		}
		deduct := min(lot.Remaining, need)
		res, err := mdb.DrugLots().UpdateOne(txCtx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": deduct}},
			bson.M{"$inc": bson.M{"remaining": -deduct}},
		)
		if err != nil {
			return Taken{}, err
		}
		if res.MatchedCount == 0 {
			return Taken{}, fmt.Errorf("insufficient lot inventory for %s", drug.Name)
		}
		cost := drug.CostPrice
		if lot.CostPrice != nil {
			cost = *lot.CostPrice
		}
		out.CostSubtotal += float64(deduct) * cost
		out.Splits = append(out.Splits, models.LotDeduction{LotID: lot.ID, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate, Qty: deduct})
		need -= deduct
	}
	if need > 0 {
		if !allowOversell {
			return Taken{}, fmt.Errorf("insufficient lot inventory for %s", drug.Name)
		}
		out.CostSubtotal += float64(need) * drug.CostPrice
		out.Oversold = need
	}
	return out, nil
}

// GiveBack returns qty units of a sale line to stock (a return or a void).
// alreadyReturned units of the line came back earlier; they are assumed to
// have used the lot-covered part first. Units the line took from real lots
// go back to those lots, even written-off ones: the goods are physically
// back. Call inside the return's or void's transaction.
func GiveBack(txCtx context.Context, mdb *db.MongoDB, item models.SaleItem, qty, alreadyReturned int) error {
	if qty <= 0 {
		return nil
	}
	res, err := mdb.Drugs().UpdateOne(txCtx, bson.M{"_id": item.DrugID}, bson.M{"$inc": bson.M{"stock": qty}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}

	lotCovered := -alreadyReturned
	for _, sp := range item.LotSplits {
		if !sp.LotID.IsZero() {
			lotCovered += sp.Qty
		}
	}
	lotCovered = min(lotCovered, qty)
	if lotCovered <= 0 {
		return nil
	}

	need, skip := lotCovered, alreadyReturned
	for _, split := range item.LotSplits {
		if need <= 0 {
			break
		}
		if split.LotID.IsZero() || split.Qty <= 0 {
			continue
		}
		available := split.Qty
		if skip > 0 {
			if skip >= available {
				skip -= available
				continue
			}
			available -= skip
			skip = 0
		}
		restore := min(available, need)
		res, err := mdb.DrugLots().UpdateOne(txCtx,
			bson.M{"_id": split.LotID, "drug_id": item.DrugID},
			bson.M{"$inc": bson.M{"remaining": restore}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			// The lot was deleted before lots were kept (ADR-0007). The units
			// are back in stock without a lot; the drift report shows them.
			log.Printf("inventory: lot %s of %s no longer exists; %d unit(s) returned to stock without a lot", split.LotID.Hex(), item.DrugName, restore)
		}
		need -= restore
	}
	return nil
}

// ReceiveLot records a new lot of goods: the lot, its stock, and settlement of
// oversold debt from it, oldest sale first. Call inside a transaction.
func ReceiveLot(txCtx context.Context, mdb *db.MongoDB, lot models.DrugLot) (models.DrugLot, error) {
	lot.Remaining = lot.Quantity
	if lot.CreatedAt.IsZero() {
		lot.CreatedAt = time.Now()
	}
	res, err := mdb.DrugLots().InsertOne(txCtx, lot)
	if err != nil {
		return models.DrugLot{}, err
	}
	lot.ID = res.InsertedID.(bson.ObjectID)
	upd, err := mdb.Drugs().UpdateOne(txCtx, bson.M{"_id": lot.DrugID}, bson.M{"$inc": bson.M{"stock": lot.Quantity}})
	if err != nil {
		return models.DrugLot{}, err
	}
	if upd.MatchedCount == 0 {
		return models.DrugLot{}, mongo.ErrNoDocuments
	}
	if _, err := settleOversold(txCtx, mdb, lot.DrugID, &lot, lot.Quantity, ""); err != nil {
		return models.DrugLot{}, err
	}
	return lot, nil
}

// settleOversold pays unsettled oversold debt of drugID from up to available
// units, oldest sale first, and returns the units used. With a lot the units
// come out of it and the sale's lot splits name it; without one (an increase
// that is not a lot, e.g. a drug with no lots) the split is marked
// "ADJUST:<reason>". drug.stock is not touched: the oversold sale already
// took it and the caller already added the new units.
func settleOversold(txCtx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, lot *models.DrugLot, available int, reason string) (int, error) {
	if available <= 0 {
		return 0, nil
	}
	cur, err := mdb.SaleItems().Find(txCtx,
		bson.M{"drug_id": drugID, "oversold_qty": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return 0, err
	}
	var items []models.SaleItem
	if err := cur.All(txCtx, &items); err != nil {
		return 0, err
	}
	used := 0
	for _, si := range items {
		if used >= available {
			break
		}
		take := min(si.OversoldQty, available-used)
		split := models.LotDeduction{LotID: bson.NilObjectID, LotNumber: "ADJUST", ExpiryDate: time.Now(), Qty: take}
		if reason != "" {
			split.LotNumber = "ADJUST:" + reason
		}
		if lot != nil {
			res, err := mdb.DrugLots().UpdateOne(txCtx,
				bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": take}},
				bson.M{"$inc": bson.M{"remaining": -take}},
			)
			if err != nil {
				return used, err
			}
			if res.MatchedCount == 0 {
				break
			}
			split = models.LotDeduction{LotID: lot.ID, LotNumber: lot.LotNumber, ExpiryDate: lot.ExpiryDate, Qty: take}
		}
		if _, err := mdb.SaleItems().UpdateOne(txCtx,
			bson.M{"_id": si.ID},
			bson.M{"$inc": bson.M{"oversold_qty": -take}, "$push": bson.M{"lot_splits": split}},
		); err != nil {
			return used, err
		}
		used += take
	}
	return used, nil
}

var errDrugNotFound = errors.New("drug not found")

func loadDrug(ctx context.Context, mdb *db.MongoDB, id bson.ObjectID) (models.Drug, error) {
	var d models.Drug
	err := mdb.Drugs().FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return d, refusal.NotFoundf(errDrugNotFound.Error())
	}
	return d, err
}
