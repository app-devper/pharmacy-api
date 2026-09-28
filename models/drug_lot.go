package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// DrugLot represents a single import batch of a drug.
// SellPrice and CostPrice are nullable — nil means "inherit from parent drug".
// Quantity = original imported amount (immutable).
// Remaining = current stock in this lot (decremented via FEFO on sale).
type DrugLot struct {
	ID         bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	DrugID     bson.ObjectID `bson:"drug_id"        json:"drug_id"`
	DrugName   string        `bson:"drug_name"      json:"drug_name"`
	LotNumber  string        `bson:"lot_number"     json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date"    json:"expiry_date"`
	ImportDate time.Time     `bson:"import_date"    json:"import_date"`
	CostPrice  *float64      `bson:"cost_price"     json:"cost_price"` // nil = use drug.CostPrice
	SellPrice  *float64      `bson:"sell_price"     json:"sell_price"` // nil = use drug.SellPrice
	Quantity   int           `bson:"quantity"       json:"quantity"`   // original import qty
	Remaining  int           `bson:"remaining"      json:"remaining"`  // current qty in this lot
	CreatedAt  time.Time     `bson:"created_at"     json:"created_at"`
	// WrittenOffAt marks a lot written off (ADR-0007). Lots are never deleted
	// once sold from, so voids and returns can give goods back to them; a
	// written-off lot is never taken from by a sale.
	WrittenOffAt *time.Time `bson:"written_off_at,omitempty" json:"written_off_at,omitempty"`
	// NoExpiry marks opening stock imported without lot data (bulk import).
	NoExpiry bool `bson:"no_expiry,omitempty" json:"no_expiry,omitempty"`
	// Origin says how the lot came into stock (ADR-0010); empty on lots made
	// before it was recorded.
	Origin string `bson:"origin,omitempty" json:"origin,omitempty"`
}

// Lot origins (ADR-0010).
const (
	LotReceived   = "received"   // goods received: a confirmed import or a lot added by hand
	LotOpening    = "opening"    // stock a drug was created with
	LotAdjustment = "adjustment" // created by a stock increase naming a new lot
)

// ExpiringLotItem is returned by GET /api/pharmacy/v1/lots/expiring.
type ExpiringLotItem struct {
	ID         bson.ObjectID `json:"id"`
	DrugID     bson.ObjectID `json:"drug_id"`
	DrugName   string        `json:"drug_name"`
	LotNumber  string        `json:"lot_number"`
	ExpiryDate time.Time     `json:"expiry_date"`
	Remaining  int           `json:"remaining"`
	DaysLeft   int           `json:"days_left"` // negative = already expired
}

// LotWriteoff records a write-off event for audit trail.
// Created by WriteoffLots when an expired/damaged lot is removed.
type LotWriteoff struct {
	ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
	DrugID     bson.ObjectID `bson:"drug_id"       json:"drug_id"`
	DrugName   string        `bson:"drug_name"     json:"drug_name"`
	LotNumber  string        `bson:"lot_number"    json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date"   json:"expiry_date"`
	Qty        int           `bson:"qty"           json:"qty"` // amount written off (positive)
	CreatedAt  time.Time     `bson:"created_at"    json:"created_at"`
	LotID      bson.ObjectID `bson:"lot_id,omitempty" json:"lot_id,omitempty"`
	// Reason is "writeoff" (default) or "deleted" for a lot entered by mistake.
	Reason string `bson:"reason,omitempty" json:"reason,omitempty"`
	// Received is, for a deleted lot, the quantity its receipt put into
	// stock; that receipt drops out of the movements view with the lot.
	// Nil on deletions recorded before it existed, which undid exactly their
	// receipt.
	Received *int   `bson:"received,omitempty" json:"received,omitempty"`
	By       string `bson:"by,omitempty"     json:"by,omitempty"`
}

// DrugLotInput is the POST body for creating a lot.
// Dates as ISO-8601 strings "YYYY-MM-DD".
type DrugLotInput struct {
	LotNumber  string   `json:"lot_number"`
	ExpiryDate string   `json:"expiry_date"` // required "YYYY-MM-DD"
	ImportDate string   `json:"import_date"` // optional, defaults to today
	CostPrice  *float64 `json:"cost_price"`  // optional override
	SellPrice  *float64 `json:"sell_price"`  // optional override
	Quantity   int      `json:"quantity"`    // required > 0
}
