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
}

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
	By     string `bson:"by,omitempty"     json:"by,omitempty"`
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
