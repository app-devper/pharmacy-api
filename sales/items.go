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
	"pharmacy-pos/backend/inventory"
	"pharmacy-pos/backend/models"
)

// Sale-line pricing and customer lookup. Stock is taken through inventory.

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
		if err := inventory.CheckAvailable(ctx, mdb, drugID, need); err != nil {
			return nil, 0, err
		}
	}

	return items, subtotal, nil
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
	taken, err := inventory.Take(ctx, mdb, item.Drug, item.Qty, item.AllowOversell)
	if err != nil {
		return err
	}
	// Compliance reconciliation: flag when the client's expected lot (captured
	// at cart checkout) differs from the first lot actually taken, most often
	// an offline sale synced after another terminal moved the FEFO queue.
	lotMismatch := item.LotSnapshot != nil && len(taken.Splits) > 0 && taken.Splits[0].LotID != item.LotSnapshot.LotID
	_, err = mdb.SaleItems().InsertOne(ctx, models.SaleItem{
		SaleID:        saleID,
		DrugID:        item.DrugID,
		DrugName:      item.Drug.Name,
		Qty:           item.Qty,
		Price:         item.Price,
		OriginalPrice: item.OriginalPrice,
		ItemDiscount:  item.ItemDiscount,
		Subtotal:      item.Subtotal,
		CostSubtotal:  taken.CostSubtotal,
		Unit:          item.Unit,
		UnitFactor:    item.UnitFactor,
		PriceTier:     item.PriceTier,
		LotSplits:     taken.Splits,
		LotSnapshot:   item.LotSnapshot,
		LotMismatch:   lotMismatch,
		OversoldQty:   taken.Oversold,
	})
	return err
}
