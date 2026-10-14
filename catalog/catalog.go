// Package catalog owns what a drug is in the Catalog context: its identity,
// its units, its price tiers and the ขย. registers it is kept on. Every way
// a drug is written (a single create, an edit, a bulk import row) goes
// through here, and so does "what does this drug sell for in this unit at
// this tier", so the rule a drug is saved with is the rule it is sold with.
//
// Stock and lots belong to inventory; a single create hands its opening lot
// to inventory.OpenStock.
package catalog

import (
	"fmt"
	"math"
	"strings"
	"time"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

const (
	// DefaultType is a drug's type when none is given.
	DefaultType = "ยาสามัญ"
	// DefaultUnit is a drug's base unit when none is given.
	DefaultUnit = "ชิ้น"

	maxNameLen = 255
	dateLayout = "2006-01-02"
)

func invalid(format string, args ...any) error { return refusal.Invalidf(fmt.Sprintf(format, args...)) }

// identity is the part of a drug every write shares: a name, a type and a
// base unit (defaulted), a trimmed barcode, and its ขย. registers.
type identity struct {
	Name, Type, Unit, Barcode string
	ReportTypes               []string
}

func identityOf(name, typ, unit, barcode string, reportTypes []string) (identity, error) {
	id := identity{
		Name:    strings.TrimSpace(name),
		Type:    strings.TrimSpace(typ),
		Unit:    strings.TrimSpace(unit),
		Barcode: strings.TrimSpace(barcode),
	}
	if id.Name == "" {
		return identity{}, invalid("name is required")
	}
	if len(id.Name) > maxNameLen {
		return identity{}, invalid("name too long (max %d)", maxNameLen)
	}
	if id.Type == "" {
		id.Type = DefaultType
	}
	if id.Unit == "" {
		id.Unit = DefaultUnit
	}
	types, err := ReportTypes(reportTypes)
	if err != nil {
		return identity{}, err
	}
	id.ReportTypes = types
	return id, nil
}

// ReportTypes are the ขย. registers a drug is kept on, in the form sales and
// compliance read them ("ky9"…"ky12"). It accepts "KY10" and "ขย.10" and
// refuses anything else: a misspelt type would silently stop the register
// being required at the counter.
func ReportTypes(raw []string) ([]string, error) {
	out := []string{}
	seen := map[compliance.Form]bool{}
	for _, r := range raw {
		s := strings.ToLower(strings.Join(strings.Fields(r), ""))
		if s == "" {
			continue
		}
		s = strings.TrimPrefix(strings.TrimPrefix(s, "ขย."), "ขย")
		if !strings.HasPrefix(s, "ky") {
			s = "ky" + s
		}
		form, ok := compliance.ParseForm(s)
		if !ok {
			return nil, invalid("report_types: %q is not a ขย. register (ky9–ky12)", strings.TrimSpace(r))
		}
		if !seen[form] {
			seen[form] = true
			out = append(out, string(form))
		}
	}
	return out, nil
}

// prices checks a drug's base-unit tiers and alt units against its base
// unit and returns them with retail (and sell_price) in step.
func prices(baseUnit string, sellPrice float64, tiers models.PriceTiers, alts []models.AltUnit) (float64, models.PriceTiers, []models.AltUnit, error) {
	if sellPrice < 0 {
		return 0, nil, nil, invalid("sell_price ต้อง >= 0")
	}
	altUnits, err := validateAltUnits(alts, baseUnit)
	if err != nil {
		return 0, nil, nil, err
	}
	priceTiers, err := validatePriceTiers(tiers, sellPrice)
	if err != nil {
		return 0, nil, nil, err
	}
	return priceTiers[models.TierRetail], priceTiers, altUnits, nil
}

// NewDrug is a single create: the drug, and the lot its opening stock goes
// into. Opening stock needs a lot (lot number, a future expiry) so the
// drug's stock is always backed by a real lot.
func NewDrug(in models.DrugInput, now time.Time, loc *time.Location) (models.Drug, *models.DrugLot, error) {
	id, err := identityOf(in.Name, in.Type, in.Unit, in.Barcode, in.ReportTypes)
	if err != nil {
		return models.Drug{}, nil, err
	}
	sellPrice, priceTiers, altUnits, err := prices(id.Unit, in.SellPrice, in.Prices, in.AltUnits)
	if err != nil {
		return models.Drug{}, nil, err
	}
	if in.CostPrice < 0 {
		return models.Drug{}, nil, invalid("cost_price ต้อง >= 0")
	}
	if in.Stock < 0 {
		return models.Drug{}, nil, invalid("stock must be >= 0")
	}
	if in.Stock > 0 && in.CreateLot == nil {
		return models.Drug{}, nil, invalid("create_lot is required when stock > 0")
	}
	initialStock := in.Stock
	var lot *models.DrugLot
	if in.CreateLot != nil {
		if lot, err = openingLot(*in.CreateLot, in.Stock, now, loc); err != nil {
			return models.Drug{}, nil, err
		}
		lot.DrugName = id.Name
		initialStock = lot.Quantity
	}
	return models.Drug{
		Name:        id.Name,
		GenericName: strings.TrimSpace(in.GenericName),
		Type:        id.Type,
		Strength:    strings.TrimSpace(in.Strength),
		Barcode:     id.Barcode,
		SellPrice:   sellPrice,
		CostPrice:   in.CostPrice,
		Stock:       initialStock,
		MinStock:    in.MinStock,
		RegNo:       strings.TrimSpace(in.RegNo),
		Unit:        id.Unit,
		ReportTypes: id.ReportTypes,
		AltUnits:    altUnits,
		Prices:      priceTiers,
		CreatedAt:   now,
	}, lot, nil
}

func openingLot(in models.DrugLotInput, stock int, now time.Time, loc *time.Location) (*models.DrugLot, error) {
	number := strings.TrimSpace(in.LotNumber)
	if number == "" {
		return nil, invalid("create_lot.lot_number is required")
	}
	if in.Quantity <= 0 {
		return nil, invalid("create_lot.quantity must be > 0")
	}
	if in.ExpiryDate == "" {
		return nil, invalid("create_lot.expiry_date is required")
	}
	expiry, err := time.ParseInLocation(dateLayout, in.ExpiryDate, loc)
	if err != nil {
		return nil, invalid("create_lot.expiry_date must be YYYY-MM-DD")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if !expiry.After(today) {
		return nil, invalid("create_lot.expiry_date must be in the future")
	}
	imported := now
	if in.ImportDate != "" {
		if imported, err = time.ParseInLocation(dateLayout, in.ImportDate, loc); err != nil {
			return nil, invalid("create_lot.import_date must be YYYY-MM-DD")
		}
	}
	if stock != 0 && stock != in.Quantity {
		return nil, invalid("stock must be 0 or equal create_lot.quantity when create_lot is provided")
	}
	return &models.DrugLot{
		LotNumber:  number,
		ExpiryDate: expiry,
		ImportDate: imported,
		CostPrice:  in.CostPrice,
		SellPrice:  in.SellPrice,
		Quantity:   in.Quantity,
		Remaining:  in.Quantity,
		CreatedAt:  now,
	}, nil
}

// ImportedDrug is one bulk-import row. Bulk stays more permissive than a
// single create: negative stock is clamped to 0 and stock needs no lot
// (inventory opens an OPENING lot for it, ADR-0007). Identity, prices and
// report types follow the same rules as everywhere else.
func ImportedDrug(in models.DrugInput, now time.Time) (models.Drug, error) {
	id, err := identityOf(in.Name, in.Type, in.Unit, in.Barcode, in.ReportTypes)
	if err != nil {
		return models.Drug{}, err
	}
	if in.SellPrice < 0 {
		return models.Drug{}, invalid("ราคาขายต้องไม่ติดลบ")
	}
	if in.CostPrice < 0 {
		return models.Drug{}, invalid("ราคาทุนต้องไม่ติดลบ")
	}
	sellPrice, priceTiers, altUnits, err := prices(id.Unit, in.SellPrice, in.Prices, in.AltUnits)
	if err != nil {
		return models.Drug{}, err
	}
	return models.Drug{
		Name:        id.Name,
		GenericName: strings.TrimSpace(in.GenericName),
		Type:        id.Type,
		Strength:    strings.TrimSpace(in.Strength),
		Barcode:     id.Barcode,
		SellPrice:   sellPrice,
		CostPrice:   in.CostPrice,
		Stock:       max(in.Stock, 0),
		MinStock:    in.MinStock,
		RegNo:       strings.TrimSpace(in.RegNo),
		Unit:        id.Unit,
		ReportTypes: id.ReportTypes,
		AltUnits:    altUnits,
		Prices:      priceTiers,
		CreatedAt:   now,
	}, nil
}

// Edit is an update as it is stored: the same identity rules as a create
// (a name is required, the base unit defaults), alt units checked against
// that base unit, and retail and sell_price in step.
func Edit(in models.DrugUpdate) (models.DrugUpdate, error) {
	id, err := identityOf(in.Name, in.Type, in.Unit, in.Barcode, in.ReportTypes)
	if err != nil {
		return models.DrugUpdate{}, err
	}
	if in.CostPrice < 0 {
		return models.DrugUpdate{}, invalid("cost_price ต้อง >= 0")
	}
	sellPrice, priceTiers, altUnits, err := prices(id.Unit, in.SellPrice, in.Prices, in.AltUnits)
	if err != nil {
		return models.DrugUpdate{}, err
	}
	in.Name, in.Type, in.Unit, in.Barcode, in.ReportTypes = id.Name, id.Type, id.Unit, id.Barcode, id.ReportTypes
	in.GenericName = strings.TrimSpace(in.GenericName)
	in.Strength = strings.TrimSpace(in.Strength)
	in.RegNo = strings.TrimSpace(in.RegNo)
	in.SellPrice, in.Prices, in.AltUnits = sellPrice, priceTiers, altUnits
	return in, nil
}

// LinePrice is what one base unit of drug sells for when sold in unit at
// tier, before any line discount, and the unit's factor. unit "" is the base
// unit; an alt unit must exist on the drug, factor (0 = the drug's) must
// match it, and qty (always in base units) must be whole alt units. Alt-unit
// prices are rounded per base unit to the satang.
func LinePrice(drug models.Drug, unit string, factor, qty int, tier string) (perBase float64, unitFactor int, err error) {
	tier = strings.TrimSpace(tier)
	if !models.IsValidPriceTier(tier) {
		return 0, 0, invalid("price_tier ของยา %s ไม่ถูกต้อง", drug.Name)
	}
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return models.ResolveTierPrice(drug.SellPrice, drug.Prices, tier), 1, nil
	}
	var alt *models.AltUnit
	for i := range drug.AltUnits {
		if drug.AltUnits[i].Name == unit {
			alt = &drug.AltUnits[i]
			break
		}
	}
	if alt == nil {
		return 0, 0, invalid("unit %q ไม่พบในยา %s", unit, drug.Name)
	}
	if factor == 0 {
		factor = alt.Factor
	} else if factor != alt.Factor {
		return 0, 0, invalid("unit_factor ไม่ตรงกับ alt_unit ของยา %s", drug.Name)
	}
	if qty%factor != 0 {
		return 0, 0, invalid("qty (%d) ต้องหารด้วย factor (%d) ลงตัว สำหรับยา %s", qty, factor, drug.Name)
	}
	perAlt := models.ResolveTierPrice(alt.SellPrice, alt.Prices, tier)
	return math.Round(perAlt/float64(factor)*100) / 100, factor, nil
}
