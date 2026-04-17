package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type DrugReturn struct {
	ID           bson.ObjectID  `bson:"_id,omitempty"         json:"id"`
	ReturnNo     string         `bson:"return_no"             json:"return_no"`
	SaleID       bson.ObjectID  `bson:"sale_id"               json:"sale_id"`
	BillNo       string         `bson:"bill_no"               json:"bill_no"`
	CustomerID   *bson.ObjectID `bson:"customer_id,omitempty" json:"customer_id,omitempty"`
	CustomerName string         `bson:"customer_name"         json:"customer_name"`
	Items        []ReturnItem   `bson:"items"                 json:"items"`
	Refund       float64        `bson:"refund"                json:"refund"`
	Reason       string         `bson:"reason"                json:"reason"`
	ReturnedAt   time.Time      `bson:"returned_at"           json:"returned_at"`
}

type ReturnItem struct {
	SaleItemID   bson.ObjectID `bson:"sale_item_id" json:"sale_item_id"`
	DrugID       bson.ObjectID `bson:"drug_id"      json:"drug_id"`
	DrugName     string        `bson:"drug_name"    json:"drug_name"`
	Qty          int           `bson:"qty"          json:"qty"`
	Price        float64       `bson:"price"        json:"price"`
	Subtotal     float64       `bson:"subtotal"     json:"subtotal"`
	CostSubtotal float64       `bson:"cost_subtotal" json:"cost_subtotal"`
}

type DrugReturnInput struct {
	Items  []ReturnItemInput `json:"items"`
	Reason string            `json:"reason"`
}

type ReturnItemInput struct {
	SaleItemID string `json:"sale_item_id"`
	Qty        int    `json:"qty"`
}
