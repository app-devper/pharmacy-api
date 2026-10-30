package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Settings is a singleton document (one per tenant) that stores shop-level
// configuration shared across the UI — store info + receipt layout.
// The handler upserts by a fixed key "singleton" so there is always at most one row.
type Settings struct {
	ID         bson.ObjectID   `bson:"_id,omitempty" json:"-"`
	Key        string          `bson:"key"           json:"-"` // always "singleton"
	Store      StoreInfo       `bson:"store"         json:"store"`
	Receipt    ReceiptSettings `bson:"receipt"       json:"receipt"`
	Stock      StockSettings   `bson:"stock"         json:"stock"`
	Pharmacist PharmacistInfo  `bson:"pharmacist"    json:"pharmacist"`
	KY         KYSettings      `bson:"ky"            json:"ky"`
	// Timezone — IANA name (e.g. "Asia/Bangkok"). Controls how "today" /
	// "this month" are computed for reports and day-bucketed filters. Empty →
	// falls back to DefaultTimezone.
	Timezone  string    `bson:"timezone,omitempty" json:"timezone"`
	UpdatedAt time.Time `bson:"updated_at"         json:"updated_at"`
}

type StoreInfo struct {
	Name    string `bson:"name"    json:"name"`
	Address string `bson:"address" json:"address"`
	Phone   string `bson:"phone"   json:"phone"`
	TaxID   string `bson:"tax_id"  json:"tax_id"`
	// LicenseNo is the shop's drug-selling licence (ใบอนุญาตขายยา, e.g.
	// "ขย. 12345/2567"), printed on the ขย. registers.
	LicenseNo string `bson:"license_no,omitempty" json:"license_no"`
}

type ReceiptSettings struct {
	Header         string `bson:"header"          json:"header"`          // tagline above items
	Footer         string `bson:"footer"          json:"footer"`          // thank-you message
	PaperWidth     string `bson:"paper_width"     json:"paper_width"`     // "58" | "80"
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
	DefaultTimezone          = "Asia/Bangkok"

	// The longest windows a shop may set (days).
	MaxReorderDays      = 365
	MaxReorderLookahead = 180
	MaxExpiringDays     = 365
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
		Timezone: DefaultTimezone,
	}
}

// SettingsInput mirrors Settings but drops internal fields (_id, key, updated_at).
type SettingsInput struct {
	Store      StoreInfo       `json:"store"`
	Receipt    ReceiptSettings `json:"receipt"`
	Stock      StockSettings   `json:"stock"`
	Pharmacist PharmacistInfo  `json:"pharmacist"`
	KY         KYSettings      `json:"ky"`
	Timezone   string          `json:"timezone"`
}

// Effective is the settings as the shop runs on them: a zero reorder window,
// lookahead or expiry window means the built-in default (there is no rate
// over zero days). A zero low-stock threshold is kept: it means "never flag
// low stock". A blank or unknown timezone means DefaultTimezone (see
// Location).
func (s Settings) Effective() Settings {
	s.Stock.ReorderDays = window(s.Stock.ReorderDays, DefaultReorderDays, MaxReorderDays)
	s.Stock.ReorderLookahead = window(s.Stock.ReorderLookahead, DefaultReorderLookahead, MaxReorderLookahead)
	s.Stock.ExpiringDays = window(s.Stock.ExpiringDays, DefaultExpiringDays, MaxExpiringDays)
	if s.Receipt.PaperWidth != "80" {
		s.Receipt.PaperWidth = "58"
	}
	if _, err := time.LoadLocation(s.Timezone); s.Timezone == "" || err != nil {
		s.Timezone = DefaultTimezone
	}
	return s
}

// window is a day window as the shop runs on it: zero or less means the
// default, and it never exceeds the longest the shop may set.
func window(days, def, max int) int {
	if days <= 0 {
		return def
	}
	return min(days, max)
}

// SettingsError is an edit the shop cannot save as sent.
type SettingsError string

func (e SettingsError) Error() string { return string(e) }

// Accept is an edit as the shop will store it: text trimmed, the refusals
// (no shop name, an unknown paper width or timezone, a negative low-stock
// threshold) returned as a SettingsError, and everything else made
// Effective, so a stored document is always one Effective leaves unchanged.
func (in SettingsInput) Accept() (Settings, error) {
	trim := strings.TrimSpace
	in.Store.Name = trim(in.Store.Name)
	in.Store.Address = trim(in.Store.Address)
	in.Store.Phone = trim(in.Store.Phone)
	in.Store.TaxID = trim(in.Store.TaxID)
	in.Store.LicenseNo = trim(in.Store.LicenseNo)
	in.Receipt.Header = trim(in.Receipt.Header)
	in.Receipt.Footer = trim(in.Receipt.Footer)
	in.Pharmacist.Name = trim(in.Pharmacist.Name)
	in.Pharmacist.LicenseNo = trim(in.Pharmacist.LicenseNo)
	in.KY.DefaultBuyerAddress = trim(in.KY.DefaultBuyerAddress)
	in.Timezone = trim(in.Timezone)

	if in.Store.Name == "" {
		return Settings{}, SettingsError("ชื่อร้านห้ามว่าง")
	}
	switch in.Receipt.PaperWidth {
	case "", "58", "80":
	default:
		return Settings{}, SettingsError("paper_width ต้องเป็น 58 หรือ 80 เท่านั้น")
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			return Settings{}, SettingsError("timezone ไม่ถูกต้อง (ต้องเป็น IANA เช่น Asia/Bangkok)")
		}
	}
	if in.Stock.LowStockThreshold < 0 {
		return Settings{}, SettingsError("low_stock_threshold ต้องไม่ติดลบ")
	}
	return Settings{
		Key:        "singleton",
		Store:      in.Store,
		Receipt:    in.Receipt,
		Stock:      in.Stock,
		Pharmacist: in.Pharmacist,
		KY:         in.KY,
		Timezone:   in.Timezone,
	}.Effective(), nil
}

// defaultLocation is DefaultTimezone, or a fixed +07:00 when the host has no
// zone database.
var defaultLocation = func() *time.Location {
	if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
		return loc
	}
	return time.FixedZone(DefaultTimezone, 7*60*60)
}()

// Location is the shop's timezone: where its days and months begin.
func (s Settings) Location() *time.Location {
	if s.Timezone == "" {
		return defaultLocation
	}
	if loc, err := time.LoadLocation(s.Timezone); err == nil {
		return loc
	}
	return defaultLocation
}
