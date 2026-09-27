package sales

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// The sale-line and stock helpers below moved unchanged from handlers/sales.go.

type preparedSaleItem struct {
	Drug          models.Drug
	DrugID        bson.ObjectID
	Qty           int     // BASE units
	Price         float64 // per BASE unit, post item-discount
	OriginalPrice float64
	ItemDiscount  float64
	Subtotal      float64
	CostSubtotal  float64
	Unit          string // alt-unit display name ("" = base)
	UnitFactor    int    // 1 = base unit; >=2 = alt
	PriceTier     string // "" | retail | regular | wholesale
	// Forwarded from SaleItemInput — lets applySaleItem compare the client's
	// expected lot against the actual FEFO deduction.
	LotSnapshot *models.LotSnapshot
	// When true, applySaleItem records any stock shortfall as OversoldQty
	// instead of failing. Reconciled against the next import lot.
	AllowOversell bool
}

func prepareSaleItems(ctx context.Context, mdb *db.MongoDB, inputs []models.SaleItemInput) ([]preparedSaleItem, float64, error) {
	items := make([]preparedSaleItem, 0, len(inputs))
	requiredByDrug := make(map[bson.ObjectID]int)
	var subtotal float64

	for _, input := range inputs {
		if input.Qty <= 0 {
			return nil, 0, fmt.Errorf("qty must be > 0")
		}
		if input.Price < 0 {
			return nil, 0, fmt.Errorf("price must be >= 0")
		}
		if input.OriginalPrice < 0 {
			return nil, 0, fmt.Errorf("original_price must be >= 0")
		}
		if input.ItemDiscount < 0 {
			return nil, 0, fmt.Errorf("item_discount must be >= 0")
		}

		drugID, err := bson.ObjectIDFromHex(input.DrugID)
		if err != nil {
			return nil, 0, err
		}

		var drug models.Drug
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
			return nil, 0, err
		}

		// Multi-unit: when the client ships a non-empty Unit, verify it matches an
		// AltUnit on the drug and that the supplied Qty (which is ALWAYS in base
		// units) is a multiple of the factor. UnitFactor = 0 or 1 = base unit.
		unit := strings.TrimSpace(input.Unit)
		factor := input.UnitFactor
		var matchedAlt *models.AltUnit
		if unit != "" {
			for i := range drug.AltUnits {
				if drug.AltUnits[i].Name == unit {
					matchedAlt = &drug.AltUnits[i]
					break
				}
			}
			if matchedAlt == nil {
				return nil, 0, fmt.Errorf("unit %q ไม่พบในยา %s", unit, drug.Name)
			}
			if factor == 0 {
				factor = matchedAlt.Factor
			} else if factor != matchedAlt.Factor {
				return nil, 0, fmt.Errorf("unit_factor ไม่ตรงกับ alt_unit ของยา %s", drug.Name)
			}
			if input.Qty%factor != 0 {
				return nil, 0, fmt.Errorf("qty (%d) ต้องหารด้วย factor (%d) ลงตัว สำหรับยา %s", input.Qty, factor, drug.Name)
			}
		} else {
			factor = 1 // base unit
		}

		// Pricing tier: authoritative price comes from the drug document, not
		// the client's claimed `original_price`. This closes the "client sets
		// tier=wholesale but sends retail amount" loophole.
		tier := strings.TrimSpace(input.PriceTier)
		if !models.IsValidPriceTier(tier) {
			return nil, 0, fmt.Errorf("price_tier ของยา %s ไม่ถูกต้อง", drug.Name)
		}
		var authoritativeOriginal float64
		if matchedAlt != nil {
			perAlt := models.ResolveTierPrice(matchedAlt.SellPrice, matchedAlt.Prices, tier)
			// Round to 2 decimal places so fractional division (e.g. ฿100/3)
			// doesn't leave long-tail floats on the SaleItem that later break
			// reprint / receipt math.
			authoritativeOriginal = math.Round(perAlt/float64(factor)*100) / 100
		} else {
			authoritativeOriginal = models.ResolveTierPrice(drug.SellPrice, drug.Prices, tier)
		}

		originalPrice := authoritativeOriginal
		itemDiscount := input.ItemDiscount
		effectivePrice := originalPrice - itemDiscount
		if effectivePrice < 0 {
			effectivePrice = 0
		}
		subtotal += effectivePrice * float64(input.Qty)
		// Oversold inputs skip the stock-availability aggregation — the apply
		// step records any shortfall as OversoldQty instead of failing.
		if !input.AllowOversell {
			requiredByDrug[drugID] += input.Qty
		}
		items = append(items, preparedSaleItem{
			Drug:          drug,
			DrugID:        drugID,
			Qty:           input.Qty,
			Price:         effectivePrice,
			OriginalPrice: originalPrice,
			ItemDiscount:  itemDiscount,
			Subtotal:      effectivePrice * float64(input.Qty),
			Unit:          unit,
			UnitFactor:    factor,
			PriceTier:     tier,
			LotSnapshot:   input.LotSnapshot,
			AllowOversell: input.AllowOversell,
		})
	}

	for drugID, need := range requiredByDrug {
		if err := ensureSaleInventoryAvailable(ctx, mdb, drugID, need); err != nil {
			return nil, 0, err
		}
	}

	return items, subtotal, nil
}

func ensureSaleInventoryAvailable(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, need int) error {
	var drug models.Drug
	if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
		return err
	}
	if drug.Stock < need {
		return fmt.Errorf("insufficient stock for %s", drug.Name)
	}

	cur, err := mdb.DrugLots().Find(ctx,
		bson.M{"drug_id": drugID, "remaining": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	totalRemaining := 0
	lotCount := 0
	for cur.Next(ctx) {
		var lot models.DrugLot
		if err := cur.Decode(&lot); err != nil {
			return err
		}
		lotCount++
		totalRemaining += lot.Remaining
	}
	if err := cur.Err(); err != nil {
		return err
	}
	if lotCount > 0 && totalRemaining < need {
		return fmt.Errorf("insufficient lot inventory for %s", drug.Name)
	}
	return nil
}

func resolveSaleCustomer(ctx context.Context, mdb *db.MongoDB, customerID *string) (*bson.ObjectID, string, error) {
	if customerID == nil || *customerID == "" {
		return nil, "", nil
	}

	oid, err := bson.ObjectIDFromHex(*customerID)
	if err != nil {
		return nil, "", err
	}

	var customer models.Customer
	if err := mdb.Customers().FindOne(ctx, bson.M{"_id": oid}).Decode(&customer); err != nil {
		return nil, "", err
	}
	return &oid, customer.Name, nil
}

func nextSaleBillNo(ctx context.Context, mdb *db.MongoDB, now time.Time) (string, error) {
	today := now.Format("060102")
	counterID := "INV-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return "", fmt.Errorf("bill number error: %w", err)
	}
	return fmt.Sprintf("INV-%s-%03d", today, counter.Seq), nil
}

func applySaleItem(ctx context.Context, mdb *db.MongoDB, saleID bson.ObjectID, item preparedSaleItem) error {
	// Oversell-aware stock decrement.
	//  • Normal path: $gte guard — refuses if stock < qty (prevents accidental
	//    negative on mis-click).
	//  • Oversell path: unconditional $inc — drug.stock may go negative. The
	//    shortfall is tracked as OversoldQty on the SaleItem and will be
	//    reconciled when a future import lands for this drug.
	if item.AllowOversell {
		if _, err := mdb.Drugs().UpdateOne(ctx,
			bson.M{"_id": item.DrugID},
			bson.M{"$inc": bson.M{"stock": -item.Qty}},
		); err != nil {
			return err
		}
	} else {
		updateResult, err := mdb.Drugs().UpdateOne(ctx,
			bson.M{"_id": item.DrugID, "stock": bson.M{"$gte": item.Qty}},
			bson.M{"$inc": bson.M{"stock": -item.Qty}},
		)
		if err != nil {
			return err
		}
		if updateResult.MatchedCount == 0 {
			return fmt.Errorf("insufficient stock for %s", item.Drug.Name)
		}
	}

	lotCur, err := mdb.DrugLots().Find(ctx,
		bson.M{"drug_id": item.DrugID, "remaining": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer lotCur.Close(ctx)

	var lots []models.DrugLot
	if err := lotCur.All(ctx, &lots); err != nil {
		return err
	}
	if len(lots) == 0 {
		// No lots available at all. In oversell mode this is expected (classic
		// "zero-inventory sale"); otherwise fall back to pre-lot legacy mode
		// and trust drug.stock only. Either way we record all of item.Qty as
		// OversoldQty when oversell was opted in, so the next import can
		// reconcile lot_splits retroactively.
		oversold := 0
		if item.AllowOversell {
			oversold = item.Qty
		}
		si := models.SaleItem{
			SaleID:        saleID,
			DrugID:        item.DrugID,
			DrugName:      item.Drug.Name,
			Qty:           item.Qty,
			Price:         item.Price,
			OriginalPrice: item.OriginalPrice,
			ItemDiscount:  item.ItemDiscount,
			Subtotal:      item.Subtotal,
			CostSubtotal:  float64(item.Qty) * item.Drug.CostPrice,
			Unit:          item.Unit,
			UnitFactor:    item.UnitFactor,
			PriceTier:     item.PriceTier,
			LotSnapshot:   item.LotSnapshot,
			OversoldQty:   oversold,
		}
		if _, err := mdb.SaleItems().InsertOne(ctx, si); err != nil {
			return err
		}
		return nil
	}

	need := item.Qty
	costSubtotal := 0.0
	splits := make([]models.LotDeduction, 0, 2)
	for _, lot := range lots {
		if need <= 0 {
			break
		}

		deduct := lot.Remaining
		if deduct > need {
			deduct = need
		}
		res, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": deduct}},
			bson.M{"$inc": bson.M{"remaining": -deduct}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("insufficient lot inventory for %s", item.Drug.Name)
		}
		lotCost := item.Drug.CostPrice
		if lot.CostPrice != nil {
			lotCost = *lot.CostPrice
		}
		costSubtotal += float64(deduct) * lotCost
		need -= deduct
		splits = append(splits, models.LotDeduction{
			LotID:      lot.ID,
			LotNumber:  lot.LotNumber,
			ExpiryDate: lot.ExpiryDate,
			Qty:        deduct,
		})
	}
	if need > 0 {
		// Ran out of lots before satisfying item.Qty. Only acceptable in
		// oversell mode — the remainder becomes an unreconciled debt that
		// the next import for this drug will absorb.
		if !item.AllowOversell {
			return fmt.Errorf("insufficient lot inventory for %s", item.Drug.Name)
		}
		costSubtotal += float64(need) * item.Drug.CostPrice
	}
	oversold := 0
	if item.AllowOversell && need > 0 {
		oversold = need
	}

	// Compliance reconciliation: flag when the client's expected lot (captured
	// at cart checkout) differs from the first lot FEFO actually pulled from.
	// This happens most commonly when an offline-queued sale syncs after
	// another terminal has shifted the FEFO queue. The sale still succeeds —
	// pharmacists just get a hint that the paper audit trail may need review.
	lotMismatch := false
	if item.LotSnapshot != nil && len(splits) > 0 {
		lotMismatch = splits[0].LotID != item.LotSnapshot.LotID
	}

	si := models.SaleItem{
		SaleID:        saleID,
		DrugID:        item.DrugID,
		DrugName:      item.Drug.Name,
		Qty:           item.Qty,
		Price:         item.Price,
		OriginalPrice: item.OriginalPrice,
		ItemDiscount:  item.ItemDiscount,
		Subtotal:      item.Subtotal,
		CostSubtotal:  costSubtotal,
		Unit:          item.Unit,
		UnitFactor:    item.UnitFactor,
		PriceTier:     item.PriceTier,
		LotSplits:     splits,
		LotSnapshot:   item.LotSnapshot,
		LotMismatch:   lotMismatch,
		OversoldQty:   oversold,
	}
	if _, err := mdb.SaleItems().InsertOne(ctx, si); err != nil {
		return err
	}

	return nil
}

func restoreSaleItemLots(ctx context.Context, mdb *db.MongoDB, item models.SaleItem, qty int, skipRealLotQty int) error {
	need := qty
	skip := skipRealLotQty
	for _, split := range item.LotSplits {
		if need <= 0 {
			break
		}
		if split.LotID.IsZero() || split.Qty <= 0 {
			continue
		}

		availableFromSplit := split.Qty
		if skip > 0 {
			if skip >= availableFromSplit {
				skip -= availableFromSplit
				continue
			}
			availableFromSplit -= skip
			skip = 0
		}
		restore := availableFromSplit
		if restore > need {
			restore = need
		}

		res, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": split.LotID, "drug_id": item.DrugID},
			bson.M{"$inc": bson.M{"remaining": restore}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("failed to restore lot inventory for %s", item.DrugName)
		}

		need -= restore
	}

	if need > 0 {
		return fmt.Errorf("failed to fully restore lot inventory for %s", item.DrugName)
	}

	return nil
}
