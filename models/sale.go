package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Sale struct {
	ID           bson.ObjectID  `bson:"_id,omitempty"  json:"id"`
	BillNo       string         `bson:"bill_no"        json:"bill_no"`
	CustomerID   *bson.ObjectID `bson:"customer_id"    json:"customer_id"`
	CustomerName string         `bson:"customer_name"  json:"customer_name"`
	Discount     float64        `bson:"discount"       json:"discount"`
	Total        float64        `bson:"total"          json:"total"`
	Received     float64        `bson:"received"       json:"received"`
	Change       float64        `bson:"change"         json:"change"`
	SoldAt       time.Time      `bson:"sold_at"        json:"sold_at"`
	Voided       bool           `bson:"voided,omitempty"       json:"voided,omitempty"`
	VoidReason   string         `bson:"void_reason,omitempty"  json:"void_reason,omitempty"`
	VoidedAt     *time.Time     `bson:"voided_at,omitempty"    json:"voided_at,omitempty"`
}

// LotDeduction records one lot the FEFO engine pulled from when fulfilling a
// SaleItem. A single item may deduct from multiple lots when the earliest
// lot's remaining quantity is smaller than the requested qty — the engine
// walks to the next-earliest until the demand is met. Stored so pharmacists
// can audit which physical lot went out the door for each sale line.
type LotDeduction struct {
	LotID      bson.ObjectID `bson:"lot_id"      json:"lot_id"`
	LotNumber  string        `bson:"lot_number"  json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date" json:"expiry_date"`
	Qty        int           `bson:"qty"         json:"qty"` // base units deducted from this lot
}

// LotSnapshot is an OPTIONAL client-side hint of which lot the cashier
// expected would be deducted at checkout time (sourced from the client's
// cached lot list). Stored verbatim so the backend can flag mismatches
// when offline-queued sales sync after other clients have shifted FEFO.
type LotSnapshot struct {
	LotID      bson.ObjectID `bson:"lot_id"      json:"lot_id"`
	LotNumber  string        `bson:"lot_number"  json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date" json:"expiry_date"`
}

type SaleItem struct {
	ID            bson.ObjectID `bson:"_id,omitempty"     json:"id"`
	SaleID        bson.ObjectID `bson:"sale_id"           json:"sale_id"`
	DrugID        bson.ObjectID `bson:"drug_id"           json:"drug_id"`
	DrugName      string        `bson:"drug_name"         json:"drug_name"`
	Qty           int           `bson:"qty"               json:"qty"` // always in BASE units
	Price         float64       `bson:"price"             json:"price"` // per BASE unit, post item-discount
	OriginalPrice float64       `bson:"original_price"    json:"original_price"`
	ItemDiscount  float64       `bson:"item_discount"     json:"item_discount"` // per-base-unit discount
	Subtotal      float64       `bson:"subtotal"          json:"subtotal"`
	CostSubtotal  float64       `bson:"cost_subtotal"     json:"cost_subtotal"`
	// Multi-unit display metadata. When set, the item was sold in an alt unit.
	// Qty/Price stay in base units; display layer computes
	// display_qty = Qty / UnitFactor, display_price = Price * UnitFactor.
	Unit       string `bson:"unit,omitempty"        json:"unit"`
	UnitFactor int    `bson:"unit_factor,omitempty" json:"unit_factor"`
	// Pricing tier applied to this line. "" = retail (default).
	PriceTier  string `bson:"price_tier,omitempty"  json:"price_tier"`
	// FEFO audit trail — which lots were actually deducted. Empty when the
	// drug has no lots at all (legacy stock-only mode).
	LotSplits    []LotDeduction `bson:"lot_splits,omitempty"    json:"lot_splits,omitempty"`
	// Client hint captured at checkout. Non-nil only when the client sent
	// one. Lets offline-queued sales flag unexpected FEFO drift on replay.
	LotSnapshot  *LotSnapshot   `bson:"lot_snapshot,omitempty"  json:"lot_snapshot,omitempty"`
	// True when a LotSnapshot was provided AND the FEFO engine deducted from
	// a different lot than the snapshot's. Used for compliance reconciliation.
	LotMismatch  bool           `bson:"lot_mismatch,omitempty"  json:"lot_mismatch,omitempty"`
}

type SaleItemInput struct {
	DrugID        string       `json:"drug_id"`
	Qty           int          `json:"qty"` // in base units
	Price         float64      `json:"price"`
	OriginalPrice float64      `json:"original_price"`
	ItemDiscount  float64      `json:"item_discount"`
	Unit          string       `json:"unit"`         // alt-unit display name, "" = base
	UnitFactor    int          `json:"unit_factor"`  // 0 or 1 = base
	PriceTier     string       `json:"price_tier"`   // "" | retail | regular | wholesale
	LotSnapshot   *LotSnapshot `json:"lot_snapshot,omitempty"` // optional FEFO hint
}

type SaleInput struct {
	CustomerID *string         `json:"customer_id"`
	Items      []SaleItemInput `json:"items"`
	Discount   float64         `json:"discount"`
	Received   float64         `json:"received"`
}

// StockUpdate is an optimistic-update hint for the client: after a sale succeeds,
// these are the new drug.stock values so the client can patch local state instead
// of re-fetching the entire drug list.
type StockUpdate struct {
	DrugID   bson.ObjectID `json:"drug_id"`
	NewStock int           `json:"new_stock"`
}

type SaleResponse struct {
	BillNo       string        `json:"bill_no"`
	Discount     float64       `json:"discount"`
	Total        float64       `json:"total"`
	Change       float64       `json:"change"`
	StockUpdates []StockUpdate `json:"stock_updates,omitempty"`
}
