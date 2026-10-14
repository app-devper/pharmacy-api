package pdf

import (
	"bytes"
	"testing"

	"pharmacy-pos/backend/models"
)

func TestHeaderNamesTheShopThatKeepsTheRegister(t *testing.T) {
	cases := []struct {
		name        string
		shop        Shop
		wantName    string
		wantDetails string
	}{
		{"full", Shop{Name: "ร้านยา ก", LicenseNo: "ขย. 123/2568", Address: "1 ถ.หลัก"}, "ร้านยา ก", "ใบอนุญาต ขย. 123/2568  |  1 ถ.หลัก"},
		{"no licence yet", Shop{Name: "ร้านยา ข", Address: "2 ถ.รอง"}, "ร้านยา ข", "2 ถ.รอง"},
		{"name only", Shop{Name: "ร้านยา ค"}, "ร้านยา ค", ""},
		{"unnamed", Shop{}, "ร้านยา", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, details := c.shop.headerLines()
			if name != c.wantName || details != c.wantDetails {
				t.Fatalf("headerLines() = %q, %q; want %q, %q", name, details, c.wantName, c.wantDetails)
			}
		})
	}
}

func TestRegistersRenderForAnyShop(t *testing.T) {
	shop := Shop{Name: "ร้านยา ทดสอบ", LicenseNo: "ขย. 1/2568"}
	b, err := GenerateKy9([]models.Ky9{{DrugName: "Pseudoephedrine", Qty: 10}}, "2026-05", shop)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b.Bytes(), []byte("%PDF")) {
		t.Fatal("not a PDF")
	}
}
