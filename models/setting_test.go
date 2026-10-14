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

func TestEffectiveCapsWindowsAndPaperWidth(t *testing.T) {
	got := Settings{Stock: StockSettings{ReorderDays: 999, ReorderLookahead: 999, ExpiringDays: 999}, Receipt: ReceiptSettings{PaperWidth: "72"}}.Effective()
	if got.Stock.ReorderDays != MaxReorderDays || got.Stock.ReorderLookahead != MaxReorderLookahead || got.Stock.ExpiringDays != MaxExpiringDays {
		t.Fatalf("windows not capped: %+v", got.Stock)
	}
	if got.Receipt.PaperWidth != "58" {
		t.Fatalf("paper width %q, want 58", got.Receipt.PaperWidth)
	}
}

func TestAcceptRefusesWhatTheShopCannotRunOn(t *testing.T) {
	ok := SettingsInput{Store: StoreInfo{Name: "ร้านยา ก"}}
	cases := map[string]func(*SettingsInput){
		"blank shop name":    func(in *SettingsInput) { in.Store.Name = "   " },
		"paper width":        func(in *SettingsInput) { in.Receipt.PaperWidth = "72" },
		"unknown timezone":   func(in *SettingsInput) { in.Timezone = "Mars/Olympus" },
		"negative threshold": func(in *SettingsInput) { in.Stock.LowStockThreshold = -1 },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			in := ok
			spoil(&in)
			if _, err := in.Accept(); err == nil {
				t.Fatal("accepted")
			} else if _, isSettings := err.(SettingsError); !isSettings {
				t.Fatalf("want a SettingsError, got %T", err)
			}
		})
	}
}

func TestAcceptedSettingsAreAlreadyEffective(t *testing.T) {
	in := SettingsInput{
		Store:      StoreInfo{Name: "  ร้านยา ก  ", Address: " 1 ถ.หลัก ", LicenseNo: " ขย. 1/2568 "},
		Stock:      StockSettings{ReorderDays: 0, ReorderLookahead: 500, ExpiringDays: 30},
		Pharmacist: PharmacistInfo{Name: " ภก. ข "},
		Timezone:   " ",
	}
	got, err := in.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if got.Store.Name != "ร้านยา ก" || got.Store.Address != "1 ถ.หลัก" || got.Store.LicenseNo != "ขย. 1/2568" || got.Pharmacist.Name != "ภก. ข" {
		t.Fatalf("text not trimmed: %+v %+v", got.Store, got.Pharmacist)
	}
	if got.Stock.ReorderDays != DefaultReorderDays || got.Stock.ReorderLookahead != MaxReorderLookahead || got.Stock.ExpiringDays != 30 {
		t.Fatalf("windows %+v", got.Stock)
	}
	if got.Timezone != DefaultTimezone || got.Receipt.PaperWidth != "58" {
		t.Fatalf("timezone %q paper %q", got.Timezone, got.Receipt.PaperWidth)
	}
	if again := got.Effective(); again.Stock != got.Stock || again.Timezone != got.Timezone || again.Receipt != got.Receipt {
		t.Fatal("an accepted edit changes when made effective")
	}
}
