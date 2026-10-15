package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Customer struct {
	ID      bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Name    string        `bson:"name"          json:"name"`
	Phone   string        `bson:"phone"         json:"phone"`
	Disease string        `bson:"disease"       json:"disease"`
	// PriceTier — default tier to pre-select in the cart when this customer
	// is chosen. "" = retail, "regular", "wholesale".
	PriceTier  string     `bson:"price_tier,omitempty" json:"price_tier"`
	TotalSpent float64    `bson:"total_spent"   json:"total_spent"`
	LastVisit  *time.Time `bson:"last_visit"    json:"last_visit"`
	CreatedAt  time.Time  `bson:"created_at"    json:"created_at"`
}

type CustomerInput struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Disease   string `json:"disease"`
	PriceTier string `json:"price_tier"`
}

// InputError is a customer or supplier the shop cannot save as sent.
type InputError string

func (e InputError) Error() string { return string(e) }

// Accept is a customer as the shop will store it: text trimmed (so the tier
// that was checked is the tier that is stored, and " 081" is the same phone
// as "081"), a name required, the disease "-" when none is given.
func (in CustomerInput) Accept() (CustomerInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Disease = strings.TrimSpace(in.Disease)
	in.PriceTier = strings.TrimSpace(in.PriceTier)
	if in.Name == "" {
		return CustomerInput{}, InputError("name is required")
	}
	if in.Disease == "" {
		in.Disease = "-"
	}
	if !IsValidPriceTier(in.PriceTier) {
		return CustomerInput{}, InputError("price_tier ต้องเป็นชื่อ tier ที่ไม่เกิน 32 ตัวอักษร หรือว่าง (= retail)")
	}
	return in, nil
}
