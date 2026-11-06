// Package sales: money is settled to the satang, once per value — see ADR-0013.
package sales

import "math"

const satangPerBaht = 100

// satang rounds a baht amount half-up to two decimal places.
func satang(x float64) float64 {
	return math.Round(x*satangPerBaht) / satangPerBaht
}
