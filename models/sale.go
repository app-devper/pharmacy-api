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

type SaleItem struct {
	ID            bson.ObjectID `bson:"_id,omitempty"     json:"id"`
	SaleID        bson.ObjectID `bson:"sale_id"           json:"sale_id"`
	DrugID        bson.ObjectID `bson:"drug_id"           json:"drug_id"`
	DrugName      string        `bson:"drug_name"         json:"drug_name"`
	Qty           int           `bson:"qty"               json:"qty"`
	Price         float64       `bson:"price"             json:"price"` // effective per-unit price (= OriginalPrice - ItemDiscount)
	OriginalPrice float64       `bson:"original_price"    json:"original_price"`
	ItemDiscount  float64       `bson:"item_discount"     json:"item_discount"` // per-unit discount
	Subtotal      float64       `bson:"subtotal"          json:"subtotal"`
	CostSubtotal  float64       `bson:"cost_subtotal"     json:"cost_subtotal"`
}

type SaleItemInput struct {
	DrugID        string  `json:"drug_id"`
	Qty           int     `json:"qty"`
	Price         float64 `json:"price"`
	OriginalPrice float64 `json:"original_price"`
	ItemDiscount  float64 `json:"item_discount"`
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
