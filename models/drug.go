package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// PriceTiers is a dynamic tier→price map. Keys are tier identifiers
// ("retail", "regular", "wholesale", or any custom tier like "vip" / "staff").
// `retail` is the conventional default tier; a missing tier falls back to
// `retail`, then to the legacy `SellPrice` — see resolveTierPrice().
//
// Legacy documents that stored the old struct shape
// `{ retail, regular, wholesale }` decode into this map transparently.
type PriceTiers map[string]float64

// Well-known tier identifiers. Any other string is also accepted.
const (
	TierRetail    = "retail"
	TierRegular   = "regular"
	TierWholesale = "wholesale"
)

// AltUnit — alternate selling unit for a drug. Example: base unit "เม็ด",
// alt unit "แผง" with factor 10 means 1 blister = 10 tablets.
// Stock + lots + reports remain in the BASE unit always; alt_units are a UI
// convenience that multiplies qty × factor before touching stock.
type AltUnit struct {
	Name      string     `bson:"name"              json:"name"`
	Factor    int        `bson:"factor"            json:"factor"`     // must be >= 2
	SellPrice float64    `bson:"sell_price"        json:"sell_price"` // = Prices.Retail (back-compat shim)
	Prices    PriceTiers `bson:"prices,omitempty"  json:"prices"`
	Barcode   string     `bson:"barcode,omitempty" json:"barcode"`    // optional; scanner lookup
	// Hidden hides this alt unit from the sell-page picker. The base unit
	// is always available — hiding an alt unit only prevents cashiers from
	// picking it while leaving historical sales intact. Default false = visible.
	Hidden    bool       `bson:"hidden,omitempty"  json:"hidden"`
}

// LotSummary is a lightweight pointer to the lot that FEFO will deduct from
// next — earliest-expiring lot with remaining > 0. Attached to Drug on list
// responses so clients can (a) show "next expiry" in the UI and (b) snapshot
// which lot they expected at checkout time for offline-queued sales.
// Not persisted on the Drug document itself; populated at read time.
type LotSummary struct {
	LotID      bson.ObjectID `bson:"lot_id"      json:"lot_id"`
	LotNumber  string        `bson:"lot_number"  json:"lot_number"`
	ExpiryDate time.Time     `bson:"expiry_date" json:"expiry_date"`
}

type Drug struct {
	ID          bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	Name        string        `bson:"name"           json:"name"`
	GenericName string        `bson:"generic_name"   json:"generic_name"`
	Type        string        `bson:"type"           json:"type"`
	Strength    string        `bson:"strength"       json:"strength"`
	Barcode     string        `bson:"barcode"        json:"barcode"`
	// bson tag stays "price" for backward compat with existing MongoDB docs.
	// JSON tag is "sell_price" so the frontend receives the new name.
	SellPrice   float64   `bson:"price"          json:"sell_price"`
	CostPrice   float64   `bson:"cost_price"     json:"cost_price"`
	Stock       int       `bson:"stock"          json:"stock"`
	MinStock    int       `bson:"min_stock"      json:"min_stock"`
	RegNo       string    `bson:"reg_no"         json:"reg_no"`
	Unit        string    `bson:"unit"           json:"unit"`
	ReportTypes []string   `bson:"report_types"   json:"report_types"`
	AltUnits    []AltUnit  `bson:"alt_units,omitempty" json:"alt_units"`
	Prices      PriceTiers `bson:"prices,omitempty"    json:"prices"`
	// Next-FEFO lot — populated on list responses only, not stored. May be
	// nil when the drug has no lots (e.g. stock-only legacy data).
	NextLot     *LotSummary `bson:"-" json:"next_lot,omitempty"`
	CreatedAt   time.Time   `bson:"created_at"     json:"created_at"`
}

type DrugInput struct {
	Name        string        `json:"name"`
	GenericName string        `json:"generic_name"`
	Type        string        `json:"type"`
	Strength    string        `json:"strength"`
	Barcode     string        `json:"barcode"`
	SellPrice   float64       `json:"sell_price"`
	CostPrice   float64       `json:"cost_price"`
	Stock       int           `json:"stock"`
	MinStock    int           `json:"min_stock"`
	RegNo       string        `json:"reg_no"`
	Unit        string        `json:"unit"`
	ReportTypes []string      `json:"report_types"`
	AltUnits    []AltUnit     `json:"alt_units"`
	Prices      PriceTiers    `json:"prices"`
	CreateLot   *DrugLotInput `json:"create_lot,omitempty"`
}

// ReorderSuggestion is returned by GET /api/drugs/reorder-suggestions.
// DaysLeft uses sentinel 9999 when AvgDailySale == 0 (never sells / no data).
type ReorderSuggestion struct {
	DrugID       string  `json:"drug_id"`
	DrugName     string  `json:"drug_name"`
	Unit         string  `json:"unit"`
	CurrentStock int     `json:"current_stock"`
	MinStock     int     `json:"min_stock"`
	QtySold      int     `json:"qty_sold"`       // total sold over period
	AvgDailySale float64 `json:"avg_daily_sale"`
	DaysLeft     float64 `json:"days_left"`      // 9999 = no sales / infinite
	SuggestedQty int     `json:"suggested_qty"`
	CostPrice    float64 `json:"cost_price"`
	SellPrice    float64 `json:"sell_price"`
}

type DrugUpdate struct {
	Name        string    `json:"name"`
	GenericName string    `json:"generic_name"`
	Type        string    `json:"type"`
	Strength    string    `json:"strength"`
	Barcode     string    `json:"barcode"`
	SellPrice   float64   `json:"sell_price"`
	CostPrice   float64   `json:"cost_price"`
	MinStock    int       `json:"min_stock"`
	RegNo       string    `json:"reg_no"`
	Unit        string    `json:"unit"`
	ReportTypes []string   `json:"report_types"`
	AltUnits    []AltUnit  `json:"alt_units"`
	Prices      PriceTiers `json:"prices"`
}
