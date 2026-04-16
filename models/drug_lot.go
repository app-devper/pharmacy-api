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
	LotNumber  string        `bson:"lot_number"     json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date"    json:"expiry_date"`
	ImportDate time.Time     `bson:"import_date"    json:"import_date"`
	CostPrice  *float64      `bson:"cost_price"     json:"cost_price"`  // nil = use drug.CostPrice
	SellPrice  *float64      `bson:"sell_price"     json:"sell_price"`  // nil = use drug.SellPrice
	Quantity   int           `bson:"quantity"       json:"quantity"`    // original import qty
	Remaining  int           `bson:"remaining"      json:"remaining"`   // current qty in this lot
	CreatedAt  time.Time     `bson:"created_at"     json:"created_at"`
}

// ExpiringLotItem is returned by GET /api/lots/expiring.
type ExpiringLotItem struct {
	ID         bson.ObjectID `json:"id"`
	DrugID     bson.ObjectID `json:"drug_id"`
	DrugName   string        `json:"drug_name"`
	LotNumber  string        `json:"lot_number"`
	ExpiryDate time.Time     `json:"expiry_date"`
	Remaining  int           `json:"remaining"`
	DaysLeft   int           `json:"days_left"` // negative = already expired
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
