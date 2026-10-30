package compliance

import (
	"testing"

	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/pdf"
)

func TestRegistersArePrintedForTheShopInSettings(t *testing.T) {
	s := models.DefaultSettings()
	s.Store.Name = "ร้านยา ก"
	s.Store.Address = "1 ถ.หลัก"
	s.Store.LicenseNo = "ขย. 123/2568"
	want := pdf.Shop{Name: "ร้านยา ก", LicenseNo: "ขย. 123/2568", Address: "1 ถ.หลัก"}
	if got := ShopFrom(s); got != want {
		t.Fatalf("ShopFrom() = %+v, want %+v", got, want)
	}
}
