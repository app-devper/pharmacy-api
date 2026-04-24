package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type StockCountInputItem struct {
	DrugID  string `json:"drug_id"`
	Counted int    `json:"counted"`
}

type StockCountInput struct {
	Note  string                `json:"note"`
	Items []StockCountInputItem `json:"items"`
}

type StockCountItem struct {
	DrugID      bson.ObjectID `bson:"drug_id" json:"drug_id"`
	DrugName    string        `bson:"drug_name" json:"drug_name"`
	Unit        string        `bson:"unit" json:"unit"`
	SystemStock int           `bson:"system_stock" json:"system_stock"`
	Counted     int           `bson:"counted" json:"counted"`
	Delta       int           `bson:"delta" json:"delta"`
}

type StockCount struct {
	ID        bson.ObjectID    `bson:"_id,omitempty" json:"id"`
	CountNo   string           `bson:"count_no" json:"count_no"`
	Note      string           `bson:"note" json:"note"`
	Items     []StockCountItem `bson:"items" json:"items"`
	CreatedAt time.Time        `bson:"created_at" json:"created_at"`
}
