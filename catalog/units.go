package catalog

import (
	"strings"

	"pharmacy-pos/backend/models"
)

// validateAltUnits trims names and enforces the multi-unit invariants:
// factor >= 2, non-empty unique names that don't clash with the base unit,
// sell_price >= 0. Returns the normalised slice or an error. Also normalises
// the PriceTiers on each alt unit (Retail falls back to SellPrice if 0).
func validateAltUnits(alts []models.AltUnit, baseUnit string) ([]models.AltUnit, error) {
	if len(alts) == 0 {
		return []models.AltUnit{}, nil
	}
	out := make([]models.AltUnit, 0, len(alts))
	seen := make(map[string]struct{}, len(alts))
	for i, a := range alts {
		a.Name = strings.TrimSpace(a.Name)
		a.Barcode = strings.TrimSpace(a.Barcode)
		if a.Name == "" {
			return nil, invalid("alt_units[%d].name is required", i)
		}
		if a.Name == baseUnit {
			return nil, invalid("alt_units[%d].name ต้องต่างจากหน่วยหลัก (%s)", i, baseUnit)
		}
		if _, dup := seen[a.Name]; dup {
			return nil, invalid("alt_units[%d].name %q ซ้ำ", i, a.Name)
		}
		seen[a.Name] = struct{}{}
		if a.Factor < 2 {
			return nil, invalid("alt_units[%d].factor ต้อง >= 2 (ถ้า = 1 ให้ใช้หน่วยหลักแทน)", i)
		}
		if a.SellPrice < 0 {
			return nil, invalid("alt_units[%d].sell_price ต้อง >= 0", i)
		}
		tiers, err := validatePriceTiers(a.Prices, a.SellPrice)
		if err != nil {
			return nil, invalid("alt_units[%d]: %s", i, err)
		}
		a.Prices = tiers
		// Keep SellPrice in sync with Retail so legacy code keeps working.
		a.SellPrice = tiers[models.TierRetail]
		out = append(out, a)
	}
	return out, nil
}

// validatePriceTiers normalises the dynamic tier map:
// - rejects negatives
// - drops entries whose value is 0 (except retail)
// - ensures retail has a value, falling back to `retailFallback` (typically SellPrice)
// - trims tier keys
func validatePriceTiers(p models.PriceTiers, retailFallback float64) (models.PriceTiers, error) {
	out := models.PriceTiers{}
	for k, v := range p {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v < 0 {
			return nil, invalid("ราคา tier %q ต้อง >= 0", k)
		}
		// Retail is always kept (even if 0 — gets overwritten by fallback below).
		// Other tiers with 0 are treated as "not set" and dropped.
		if v == 0 && k != models.TierRetail {
			continue
		}
		out[k] = v
	}
	if out[models.TierRetail] == 0 && retailFallback > 0 {
		out[models.TierRetail] = retailFallback
	}
	return out, nil
}
