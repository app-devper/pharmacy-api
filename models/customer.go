package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Customer struct {
	ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Name       string        `bson:"name"          json:"name"`
	Phone      string        `bson:"phone"         json:"phone"`
	Disease    string        `bson:"disease"       json:"disease"`
	// PriceTier — default tier to pre-select in the cart when this customer
	// is chosen. "" = retail, "regular", "wholesale".
	PriceTier  string        `bson:"price_tier,omitempty" json:"price_tier"`
	TotalSpent float64       `bson:"total_spent"   json:"total_spent"`
	LastVisit  *time.Time    `bson:"last_visit"    json:"last_visit"`
	CreatedAt  time.Time     `bson:"created_at"    json:"created_at"`
}

type CustomerInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Disease   string `json:"disease"`
	PriceTier string `json:"price_tier"`
}
