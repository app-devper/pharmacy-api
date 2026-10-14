package models

import (
	"strings"
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

// Accept is a supplier as the shop will store it: text trimmed, so the
// unique name index sees "ABC" and "ABC " as the same supplier, and a name
// required.
func (in SupplierInput) Accept() (SupplierInput, error) {
	trim := strings.TrimSpace
	in.Name = trim(in.Name)
	in.ContactName = trim(in.ContactName)
	in.Phone = trim(in.Phone)
	in.Address = trim(in.Address)
	in.TaxID = trim(in.TaxID)
	in.Notes = trim(in.Notes)
	if in.Name == "" {
		return SupplierInput{}, InputError("name is required")
	}
	return in, nil
}
