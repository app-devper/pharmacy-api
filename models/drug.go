package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Drug struct {
	ID          bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	Name        string        `bson:"name"           json:"name"`
	GenericName string        `bson:"generic_name"   json:"generic_name"`
	Type        string        `bson:"type"           json:"type"`
	Strength    string        `bson:"strength"       json:"strength"`
	Barcode     string        `bson:"barcode"        json:"barcode"`
	// bson tag stays "price" for backward compat with existing MongoDB docs.
	// JSON tag is "sell_price" so the frontend receives the new name.
	SellPrice   float64       `bson:"price"          json:"sell_price"`
	CostPrice   float64       `bson:"cost_price"     json:"cost_price"`
	Stock       int           `bson:"stock"          json:"stock"`
	MinStock    int           `bson:"min_stock"      json:"min_stock"`
	RegNo       string        `bson:"reg_no"         json:"reg_no"`
	Unit        string        `bson:"unit"           json:"unit"`
	ReportTypes []string      `bson:"report_types"   json:"report_types"`
	CreatedAt   time.Time     `bson:"created_at"     json:"created_at"`
}

type DrugInput struct {
	Name        string   `json:"name"`
	GenericName string   `json:"generic_name"`
	Type        string   `json:"type"`
	Strength    string   `json:"strength"`
	Barcode     string   `json:"barcode"`
	SellPrice   float64  `json:"sell_price"`
	CostPrice   float64  `json:"cost_price"`
	Stock       int      `json:"stock"`
	MinStock    int      `json:"min_stock"`
	RegNo       string   `json:"reg_no"`
	Unit        string   `json:"unit"`
	ReportTypes []string `json:"report_types"`
}

type DrugUpdate struct {
	Name        string   `json:"name"`
	GenericName string   `json:"generic_name"`
	Type        string   `json:"type"`
	Strength    string   `json:"strength"`
	Barcode     string   `json:"barcode"`
	SellPrice   float64  `json:"sell_price"`
	CostPrice   float64  `json:"cost_price"`
	MinStock    int      `json:"min_stock"`
	RegNo       string   `json:"reg_no"`
	Unit        string   `json:"unit"`
	ReportTypes []string `json:"report_types"`
}
