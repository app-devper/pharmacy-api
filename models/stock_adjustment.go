package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Valid adjustment reasons.
var AdjustmentReasons = []string{"นับสต็อก", "ยาเสียหาย", "ยาหมดอายุ", "สูญหาย", "อื่นๆ"}

// StockAdjustmentInput is the request body for creating an adjustment.
type StockAdjustmentInput struct {
	Delta  int    `json:"delta"`  // non-zero
	Reason string `json:"reason"` // one of AdjustmentReasons
	Note   string `json:"note"`   // optional
	// Lot receives an increase on a lot-tracked drug (ADR-0007). Omitted, the
	// latest-expiring lot is assumed and the adjustment is flagged.
	Lot *LotTarget `json:"lot,omitempty"`
}

// LotTarget names the lot an increase goes into: an existing lot, or a new
// lot with its number and expiry.
type LotTarget struct {
	LotID      string `json:"lot_id,omitempty"`
	LotNumber  string `json:"lot_number,omitempty"`
	ExpiryDate string `json:"expiry_date,omitempty"` // YYYY-MM-DD, with LotNumber
}

// StockAdjustment is the audit log document stored in MongoDB.
type StockAdjustment struct {
	ID        bson.ObjectID `bson:"_id,omitempty" json:"id"`
	DrugID    bson.ObjectID `bson:"drug_id"       json:"drug_id"`
	DrugName  string        `bson:"drug_name"     json:"drug_name"`
	Delta     int           `bson:"delta"         json:"delta"`
	Before    int           `bson:"before"        json:"before"`
	After     int           `bson:"after"         json:"after"`
	Reason    string        `bson:"reason"        json:"reason"`
	Note      string        `bson:"note"          json:"note"`
	CreatedAt time.Time     `bson:"created_at"    json:"created_at"`
	// Lots records which lots the change was applied to.
	Lots []LotDeduction `bson:"lots,omitempty" json:"lots,omitempty"`
	// LotAssumed: an increase with no lot named went to the latest-expiring lot.
	LotAssumed bool   `bson:"lot_assumed,omitempty" json:"lot_assumed,omitempty"`
	By         string `bson:"by,omitempty"          json:"by,omitempty"`
}
