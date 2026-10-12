package compliance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// The register rules, through the interface callers use. A refused row is
// refused before anything is written, so these need no database.

var at = time.Date(2026, 5, 17, 9, 30, 0, 0, time.UTC)

func wantRefused(t *testing.T, err error, field string) {
	t.Helper()
	var r *refusal.Error
	if !errors.As(err, &r) || r.Kind != refusal.Invalid {
		t.Fatalf("want an Invalid refusal naming %s, got %v", field, err)
	}
	if !strings.Contains(r.Msg, field) {
		t.Fatalf("refusal %q does not name %s", r.Msg, field)
	}
}

func TestManualRowsAreRefusedByTheirRegisterRules(t *testing.T) {
	ctx := context.Background()
	ky9 := func(f func(*models.Ky9Input)) error {
		in := models.Ky9Input{Date: "2026-05-17", DrugName: "Pseudoephedrine", Qty: 100, PricePerUnit: 1.5}
		f(&in)
		_, err := RecordKy9(ctx, nil, in, at)
		return err
	}
	ky10 := func(f func(*models.Ky10Input)) error {
		in := models.Ky10Input{Date: "2026-05-17", DrugName: "Phenobarbital", Qty: 30, BuyerName: "นาย ก", BuyerAddress: "BKK"}
		f(&in)
		_, err := RecordKy10(ctx, nil, in, Defaults{}, at)
		return err
	}
	ky11 := func(f func(*models.Ky11Input)) error {
		in := models.Ky11Input{Date: "2026-05-17", DrugName: "Codeine", Qty: 50, BuyerName: "นาง ข", Purpose: "ไอ", Pharmacist: "Pharm A"}
		f(&in)
		_, err := RecordKy11(ctx, nil, in, Defaults{}, at)
		return err
	}
	ky12 := func(f func(*models.Ky12Input)) error {
		in := models.Ky12Input{Date: "2026-05-17", DrugName: "Methadone", Qty: 10, RxNo: "RX-2", PatientName: "นาย ค", Doctor: "Dr B", TotalValue: 250}
		f(&in)
		_, err := RecordKy12(ctx, nil, in, at)
		return err
	}
	cases := []struct {
		name  string
		err   error
		field string
	}{
		{"ky9 blank date", ky9(func(in *models.Ky9Input) { in.Date = "  " }), "date"},
		{"ky9 date not a calendar date", ky9(func(in *models.Ky9Input) { in.Date = "17/05/2026" }), "date"},
		{"ky9 impossible date", ky9(func(in *models.Ky9Input) { in.Date = "2026-02-30" }), "date"},
		{"ky9 blank drug", ky9(func(in *models.Ky9Input) { in.DrugName = "" }), "drug_name"},
		{"ky9 zero qty", ky9(func(in *models.Ky9Input) { in.Qty = 0 }), "qty"},
		{"ky9 negative qty", ky9(func(in *models.Ky9Input) { in.Qty = -3 }), "qty"},
		{"ky9 negative price", ky9(func(in *models.Ky9Input) { in.PricePerUnit = -0.01 }), "price_per_unit"},
		{"ky10 blank buyer", ky10(func(in *models.Ky10Input) { in.BuyerName = "" }), "buyer_name"},
		{"ky10 blank address", ky10(func(in *models.Ky10Input) { in.BuyerAddress = "   " }), "buyer_address"},
		{"ky10 zero qty", ky10(func(in *models.Ky10Input) { in.Qty = 0 }), "qty"},
		{"ky10 negative balance", ky10(func(in *models.Ky10Input) { in.Balance = -1 }), "balance"},
		{"ky11 blank date", ky11(func(in *models.Ky11Input) { in.Date = "" }), "date"},
		{"ky11 blank drug", ky11(func(in *models.Ky11Input) { in.DrugName = "" }), "drug_name"},
		{"ky11 blank buyer", ky11(func(in *models.Ky11Input) { in.BuyerName = "" }), "buyer_name"},
		{"ky11 blank purpose", ky11(func(in *models.Ky11Input) { in.Purpose = "" }), "purpose"},
		{"ky11 blank pharmacist", ky11(func(in *models.Ky11Input) { in.Pharmacist = "" }), "pharmacist"},
		{"ky12 blank rx", ky12(func(in *models.Ky12Input) { in.RxNo = "" }), "rx_no"},
		{"ky12 blank patient", ky12(func(in *models.Ky12Input) { in.PatientName = "" }), "patient_name"},
		{"ky12 blank doctor", ky12(func(in *models.Ky12Input) { in.Doctor = "" }), "doctor"},
		{"ky12 negative value", ky12(func(in *models.Ky12Input) { in.TotalValue = -0.5 }), "total_value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantRefused(t, c.err, c.field) })
	}
}

func TestACaptureLackingANeededFormIsRefused(t *testing.T) {
	c := models.SaleKyCapture{Ky12: &models.Ky12Capture{RxNo: "RX", PatientName: "P", Doctor: "D"}}
	_, err := PrepareCapture(c, []Form{Ky10, Ky12}, Defaults{})
	wantRefused(t, err, "ขย.10: buyer_name")
}

func TestACaptureIsCheckedOnlyForTheFormsTheSaleNeeds(t *testing.T) {
	c := models.SaleKyCapture{Ky12: &models.Ky12Capture{RxNo: "RX", PatientName: "P", Doctor: "D"}}
	if _, err := PrepareCapture(c, []Form{Ky12}, Defaults{}); err != nil {
		t.Fatal(err)
	}
}

func TestTheShopDefaultsFillWhatACaptureLeavesEmpty(t *testing.T) {
	c := models.SaleKyCapture{
		Ky10: &models.Ky10Capture{BuyerName: " สมชาย "},
		Ky11: &models.Ky11Capture{BuyerName: "B", Purpose: "ไอ"},
		Ky12: &models.Ky12Capture{RxNo: "RX", PatientName: "P", Doctor: "D"},
	}
	got, err := PrepareCapture(c, []Form{Ky10, Ky11, Ky12}, Defaults{BuyerAddress: "ร้าน", Pharmacist: "ภญ.ก"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Ky10.BuyerName != "สมชาย" || got.Ky10.BuyerAddress != "ร้าน" || got.Ky11.Pharmacist != "ภญ.ก" || got.Ky12.Status != PaidStatus {
		t.Fatalf("capture %+v %+v %+v", *got.Ky10, *got.Ky11, *got.Ky12)
	}
	if c.Ky10.BuyerName != " สมชาย " {
		t.Fatal("PrepareCapture changed the caller's capture")
	}
}

func TestAMalformedMonthIsRefused(t *testing.T) {
	for _, m := range []string{"2026", "2026-5", "2026-13", "05-2026", "2026-05-01"} {
		_, err := Month[models.Ky9](context.Background(), nil, m, NewestFirst)
		wantRefused(t, err, "month")
	}
}

func TestFormsParseFromReportTypesAndPaths(t *testing.T) {
	for in, want := range map[string]Form{"KY10": Ky10, " ky12 ": Ky12, "ky9": Ky9} {
		if f, ok := ParseForm(in); !ok || f != want {
			t.Fatalf("ParseForm(%q) = %q, %v", in, f, ok)
		}
	}
	if _, ok := ParseForm("ky13"); ok {
		t.Fatal("ky13 is not a register")
	}
	if Ky10.Label() != "ขย.10" {
		t.Fatalf("label %q", Ky10.Label())
	}
}
