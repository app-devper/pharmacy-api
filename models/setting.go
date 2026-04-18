package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Settings is a singleton document (one per tenant) that stores shop-level
// configuration shared across the UI — store info + receipt layout.
// The handler upserts by a fixed key "singleton" so there is always at most one row.
type Settings struct {
	ID         bson.ObjectID      `bson:"_id,omitempty" json:"-"`
	Key        string             `bson:"key"           json:"-"` // always "singleton"
	Store      StoreInfo          `bson:"store"         json:"store"`
	Receipt    ReceiptSettings    `bson:"receipt"       json:"receipt"`
	Stock      StockSettings      `bson:"stock"         json:"stock"`
	Pharmacist PharmacistInfo     `bson:"pharmacist"    json:"pharmacist"`
	KY         KYSettings         `bson:"ky"            json:"ky"`
	UpdatedAt  time.Time          `bson:"updated_at"    json:"updated_at"`
}

type StoreInfo struct {
	Name    string `bson:"name"    json:"name"`
	Address string `bson:"address" json:"address"`
	Phone   string `bson:"phone"   json:"phone"`
	TaxID   string `bson:"tax_id"  json:"tax_id"`
}

type ReceiptSettings struct {
	Header         string `bson:"header"          json:"header"`           // tagline above items
	Footer         string `bson:"footer"          json:"footer"`           // thank-you message
	PaperWidth     string `bson:"paper_width"     json:"paper_width"`      // "58" | "80"
	ShowPharmacist bool   `bson:"show_pharmacist" json:"show_pharmacist"` // pulls name from Settings.Pharmacist
}

// PharmacistInfo — single source of truth for the shop's pharmacist.
// Used in: Receipt footer, KY.11 auto-fill, KY.10 pharmacist field if any.
type PharmacistInfo struct {
	Name      string `bson:"name"       json:"name"`       // ชื่อ-นามสกุล
	LicenseNo string `bson:"license_no" json:"license_no"` // เลขที่ใบประกอบวิชาชีพ
}

// KYSettings — behavior toggles for the KY (ขย.) compliance flow.
type KYSettings struct {
	SkipAuto            bool   `bson:"skip_auto"             json:"skip_auto"`             // true = bypass KySaleModal entirely
	DefaultBuyerAddress string `bson:"default_buyer_address" json:"default_buyer_address"` // pre-fill ky10 buyer address
}

// StockSettings control inventory-related thresholds used across the app.
// Zero/invalid values fall back to the built-in defaults (see DefaultSettings).
type StockSettings struct {
	LowStockThreshold int `bson:"low_stock_threshold" json:"low_stock_threshold"` // default 20, used when drug.min_stock = 0
	ReorderDays       int `bson:"reorder_days"        json:"reorder_days"`        // default 30, reorder lookback window
	ReorderLookahead  int `bson:"reorder_lookahead"   json:"reorder_lookahead"`   // default 14, target cover days
	ExpiringDays      int `bson:"expiring_days"       json:"expiring_days"`       // default 60, expiry-alert window
}

// Built-in defaults used whenever a settings document is missing or fields are
// zero-valued. Also exported so handlers can compute effective thresholds at
// request time (e.g. LowStock filter falls back to DefaultLowStockThreshold
// when `stock.low_stock_threshold` is 0).
const (
	DefaultLowStockThreshold = 20
	DefaultReorderDays       = 30
	DefaultReorderLookahead  = 14
	DefaultExpiringDays      = 60
)

// DefaultSettings returns the values used when no document exists yet.
func DefaultSettings() Settings {
	return Settings{
		Key: "singleton",
		Store: StoreInfo{
			Name:    "ร้านยา",
			Address: "",
			Phone:   "",
			TaxID:   "",
		},
		Receipt: ReceiptSettings{
			Header:         "",
			Footer:         "ขอบคุณที่ใช้บริการ",
			PaperWidth:     "58",
			ShowPharmacist: false,
		},
		Stock: StockSettings{
			LowStockThreshold: DefaultLowStockThreshold,
			ReorderDays:       DefaultReorderDays,
			ReorderLookahead:  DefaultReorderLookahead,
			ExpiringDays:      DefaultExpiringDays,
		},
		Pharmacist: PharmacistInfo{
			Name:      "",
			LicenseNo: "",
		},
		KY: KYSettings{
			SkipAuto:            false,
			DefaultBuyerAddress: "",
		},
	}
}

// SettingsInput mirrors Settings but drops internal fields (_id, key, updated_at).
type SettingsInput struct {
	Store      StoreInfo       `json:"store"`
	Receipt    ReceiptSettings `json:"receipt"`
	Stock      StockSettings   `json:"stock"`
	Pharmacist PharmacistInfo  `json:"pharmacist"`
	KY         KYSettings      `json:"ky"`
}
