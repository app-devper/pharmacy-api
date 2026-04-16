package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SupplierInput is the request body for Create and Update.
type SupplierInput struct {
	Name        string `json:"name"`
	ContactName string `json:"contact_name"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	TaxID       string `json:"tax_id"`
	Notes       string `json:"notes"`
}

// Supplier is the full document stored in MongoDB.
type Supplier struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Name        string        `bson:"name"          json:"name"`
	ContactName string        `bson:"contact_name"  json:"contact_name"`
	Phone       string        `bson:"phone"         json:"phone"`
	Address     string        `bson:"address"       json:"address"`
	TaxID       string        `bson:"tax_id"        json:"tax_id"`
	Notes       string        `bson:"notes"         json:"notes"`
	CreatedAt   time.Time     `bson:"created_at"    json:"created_at"`
}
