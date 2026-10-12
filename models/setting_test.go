package models

import "testing"

func TestEffectiveSettingsFallBackToTheBuiltInDefaults(t *testing.T) {
	cases := []struct {
		name string
		in   Settings
		want StockSettings
		tz   string
	}{
		{"zero windows take defaults", Settings{},
			StockSettings{LowStockThreshold: 0, ReorderDays: DefaultReorderDays, ReorderLookahead: DefaultReorderLookahead, ExpiringDays: DefaultExpiringDays}, DefaultTimezone},
		{"a zero low-stock threshold is kept", Settings{Stock: StockSettings{LowStockThreshold: 0, ReorderDays: 7, ReorderLookahead: 3, ExpiringDays: 90}},
			StockSettings{LowStockThreshold: 0, ReorderDays: 7, ReorderLookahead: 3, ExpiringDays: 90}, DefaultTimezone},
		{"negative windows take defaults", Settings{Stock: StockSettings{LowStockThreshold: 5, ReorderDays: -1, ReorderLookahead: -1, ExpiringDays: -1}},
			StockSettings{LowStockThreshold: 5, ReorderDays: DefaultReorderDays, ReorderLookahead: DefaultReorderLookahead, ExpiringDays: DefaultExpiringDays}, DefaultTimezone},
		{"an unknown timezone takes the default", Settings{Timezone: "Mars/Olympus"},
			StockSettings{ReorderDays: DefaultReorderDays, ReorderLookahead: DefaultReorderLookahead, ExpiringDays: DefaultExpiringDays}, DefaultTimezone},
		{"a known timezone is kept", Settings{Timezone: "Asia/Tokyo"},
			StockSettings{ReorderDays: DefaultReorderDays, ReorderLookahead: DefaultReorderLookahead, ExpiringDays: DefaultExpiringDays}, "Asia/Tokyo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in.Effective()
			if got.Stock != c.want || got.Timezone != c.tz {
				t.Fatalf("effective %+v %q, want %+v %q", got.Stock, got.Timezone, c.want, c.tz)
			}
			if got.Location().String() != c.tz {
				t.Fatalf("location %s, want %s", got.Location(), c.tz)
			}
		})
	}
}

func TestDefaultSettingsAreAlreadyEffective(t *testing.T) {
	d := DefaultSettings()
	if d.Effective().Stock != d.Stock || d.Effective().Timezone != d.Timezone {
		t.Fatal("the built-in defaults change when made effective")
	}
}
