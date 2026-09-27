package models

import "strings"

// ResolveTierPrice returns the price for tier from p, falling back to the
// retail tier and then to base.
func ResolveTierPrice(base float64, p PriceTiers, tier string) float64 {
	if tier != "" && tier != TierRetail {
		if v, ok := p[tier]; ok && v > 0 {
			return v
		}
	}
	if v, ok := p[TierRetail]; ok && v > 0 {
		return v
	}
	return base
}

// IsValidPriceTier accepts any string (dynamic tiers), rejecting only control
// characters and overly long names. "" is allowed and means "retail".
func IsValidPriceTier(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return true
	}
	if len(t) > 32 {
		return false
	}
	for _, r := range t {
		if r < 32 {
			return false
		}
	}
	return true
}
