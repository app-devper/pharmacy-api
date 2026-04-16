package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ขย.9 — บัญชีการซื้อยา
type Ky9 struct {
	ID           bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	Date         string        `bson:"date"           json:"date"`
	DrugName     string        `bson:"drug_name"      json:"drug_name"`
	RegNo        string        `bson:"reg_no"         json:"reg_no"`
	Unit         string        `bson:"unit"           json:"unit"`
	Qty          int           `bson:"qty"            json:"qty"`
	PricePerUnit float64       `bson:"price_per_unit" json:"price_per_unit"`
	TotalValue   float64       `bson:"total_value"    json:"total_value"`
	Seller       string        `bson:"seller"         json:"seller"`
	InvoiceNo    string        `bson:"invoice_no"     json:"invoice_no"`
	CreatedAt    time.Time     `bson:"created_at"     json:"created_at"`
}

type Ky9Input struct {
	Date         string  `json:"date"`
	DrugName     string  `json:"drug_name"`
	RegNo        string  `json:"reg_no"`
	Unit         string  `json:"unit"`
	Qty          int     `json:"qty"`
	PricePerUnit float64 `json:"price_per_unit"`
	Seller       string  `json:"seller"`
	InvoiceNo    string  `json:"invoice_no"`
}

// ขย.10 — บัญชีการขายยาควบคุมพิเศษ
type Ky10 struct {
	ID           bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	Date         string        `bson:"date"           json:"date"`
	DrugName     string        `bson:"drug_name"      json:"drug_name"`
	RegNo        string        `bson:"reg_no"         json:"reg_no"`
	Qty          int           `bson:"qty"            json:"qty"`
	Unit         string        `bson:"unit"           json:"unit"`
	BuyerName    string        `bson:"buyer_name"     json:"buyer_name"`
	BuyerAddress string        `bson:"buyer_address"  json:"buyer_address"`
	RxNo         string        `bson:"rx_no"          json:"rx_no"`
	Doctor       string        `bson:"doctor"         json:"doctor"`
	Balance      int           `bson:"balance"        json:"balance"`
	CreatedAt    time.Time     `bson:"created_at"     json:"created_at"`
}

type Ky10Input struct {
	Date         string `json:"date"`
	DrugName     string `json:"drug_name"`
	RegNo        string `json:"reg_no"`
	Qty          int    `json:"qty"`
	Unit         string `json:"unit"`
	BuyerName    string `json:"buyer_name"`
	BuyerAddress string `json:"buyer_address"`
	RxNo         string `json:"rx_no"`
	Doctor       string `json:"doctor"`
	Balance      int    `json:"balance"`
}

// ขย.11 — บัญชีการขายยาอันตราย
type Ky11 struct {
	ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Date       string        `bson:"date"          json:"date"`
	DrugName   string        `bson:"drug_name"     json:"drug_name"`
	RegNo      string        `bson:"reg_no"        json:"reg_no"`
	Qty        int           `bson:"qty"           json:"qty"`
	Unit       string        `bson:"unit"          json:"unit"`
	BuyerName  string        `bson:"buyer_name"    json:"buyer_name"`
	Purpose    string        `bson:"purpose"       json:"purpose"`
	Pharmacist string        `bson:"pharmacist"    json:"pharmacist"`
	CreatedAt  time.Time     `bson:"created_at"    json:"created_at"`
}

type Ky11Input struct {
	Date       string `json:"date"`
	DrugName   string `json:"drug_name"`
	RegNo      string `json:"reg_no"`
	Qty        int    `json:"qty"`
	Unit       string `json:"unit"`
	BuyerName  string `json:"buyer_name"`
	Purpose    string `json:"purpose"`
	Pharmacist string `json:"pharmacist"`
}

// ขย.12 — บัญชีการขายยาตามใบสั่งแพทย์
type Ky12 struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Date        string        `bson:"date"          json:"date"`
	RxNo        string        `bson:"rx_no"         json:"rx_no"`
	PatientName string        `bson:"patient_name"  json:"patient_name"`
	Doctor      string        `bson:"doctor"        json:"doctor"`
	Hospital    string        `bson:"hospital"      json:"hospital"`
	DrugName    string        `bson:"drug_name"     json:"drug_name"`
	Qty         int           `bson:"qty"           json:"qty"`
	Unit        string        `bson:"unit"          json:"unit"`
	TotalValue  float64       `bson:"total_value"   json:"total_value"`
	Status      string        `bson:"status"        json:"status"`
	CreatedAt   time.Time     `bson:"created_at"    json:"created_at"`
}

type Ky12Input struct {
	Date        string  `json:"date"`
	RxNo        string  `json:"rx_no"`
	PatientName string  `json:"patient_name"`
	Doctor      string  `json:"doctor"`
	Hospital    string  `json:"hospital"`
	DrugName    string  `json:"drug_name"`
	Qty         int     `json:"qty"`
	Unit        string  `json:"unit"`
	TotalValue  float64 `json:"total_value"`
	Status      string  `json:"status"`
}
