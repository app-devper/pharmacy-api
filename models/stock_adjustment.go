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
}
