package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// POItemInput is what the frontend sends (drug_id as hex string).
// Converted to POItem in the handler.
type POItemInput struct {
	DrugID     string   `json:"drug_id"`
	DrugName   string   `json:"drug_name"`   // auto-filled if empty
	LotNumber  string   `json:"lot_number"`
	ExpiryDate string   `json:"expiry_date"` // "YYYY-MM-DD"
	Qty        int      `json:"qty"`
	CostPrice  float64  `json:"cost_price"`
	SellPrice  *float64 `json:"sell_price"` // nil = use drug default
}

// POItem is the stored version (drug_id as ObjectID).
type POItem struct {
	DrugID     bson.ObjectID `bson:"drug_id"     json:"drug_id"`
	DrugName   string        `bson:"drug_name"   json:"drug_name"`
	LotNumber  string        `bson:"lot_number"  json:"lot_number"`
	ExpiryDate string        `bson:"expiry_date" json:"expiry_date"` // stored as "YYYY-MM-DD"
	Qty        int           `bson:"qty"         json:"qty"`
	CostPrice  float64       `bson:"cost_price"  json:"cost_price"`
	SellPrice  *float64      `bson:"sell_price"  json:"sell_price"`
}

// POInput is the request body for Create and Update.
type POInput struct {
	Supplier    string        `json:"supplier"`
	InvoiceNo   string        `json:"invoice_no"`
	ReceiveDate string        `json:"receive_date"` // "YYYY-MM-DD"
	Notes       string        `json:"notes"`
	Items       []POItemInput `json:"items"`
}

// PurchaseOrder is the full document (items embedded).
type PurchaseOrder struct {
	ID          bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	DocNo       string        `bson:"doc_no"         json:"doc_no"`
	Supplier    string        `bson:"supplier"       json:"supplier"`
	InvoiceNo   string        `bson:"invoice_no"     json:"invoice_no"`
	ReceiveDate time.Time     `bson:"receive_date"   json:"receive_date"`
	Items       []POItem      `bson:"items"          json:"items"`
	ItemCount   int           `bson:"item_count"     json:"item_count"`   // denormalized
	TotalCost   float64       `bson:"total_cost"     json:"total_cost"`   // sum(qty*cost_price)
	Status      string        `bson:"status"         json:"status"`       // "draft" | "confirmed"
	Notes       string        `bson:"notes"          json:"notes"`
	CreatedAt   time.Time     `bson:"created_at"     json:"created_at"`
	ConfirmedAt *time.Time    `bson:"confirmed_at"   json:"confirmed_at"`
}

// PurchaseOrderSummary is returned by the List endpoint (items omitted for performance).
type PurchaseOrderSummary struct {
	ID          bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	DocNo       string        `bson:"doc_no"         json:"doc_no"`
	Supplier    string        `bson:"supplier"       json:"supplier"`
	InvoiceNo   string        `bson:"invoice_no"     json:"invoice_no"`
	ReceiveDate time.Time     `bson:"receive_date"   json:"receive_date"`
	ItemCount   int           `bson:"item_count"     json:"item_count"`
	TotalCost   float64       `bson:"total_cost"     json:"total_cost"`
	Status      string        `bson:"status"         json:"status"`
	Notes       string        `bson:"notes"          json:"notes"`
	CreatedAt   time.Time     `bson:"created_at"     json:"created_at"`
	ConfirmedAt *time.Time    `bson:"confirmed_at"   json:"confirmed_at"`
}
