package sales

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/catalog"
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

		// The authoritative price comes from the drug (catalog), not the
		// client's claimed original_price: a client cannot claim the
		// wholesale tier and send the retail amount.
		unit := strings.TrimSpace(input.Unit)
		tier := strings.TrimSpace(input.PriceTier)
		authoritativeOriginal, factor, err := catalog.LinePrice(drug, unit, input.UnitFactor, input.Qty, tier)
		if err != nil {
			return nil, 0, err
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
