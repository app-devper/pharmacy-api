package handlers

import (
	"strings"
	"testing"

	"pharmacy-pos/backend/models"
)

func validKy9() models.Ky9Input {
	return models.Ky9Input{
		Date: "2026-05-17", DrugName: "Pseudoephedrine", Qty: 100, PricePerUnit: 1.5,
	}
}

func validKy10() models.Ky10Input {
	return models.Ky10Input{
		Date: "2026-05-17", DrugName: "Phenobarbital", Qty: 30,
		BuyerName: "นาย ก", BuyerAddress: "BKK", Balance: 0,
	}
}

func validKy11() models.Ky11Input {
	return models.Ky11Input{
		Date: "2026-05-17", DrugName: "Codeine", Qty: 50,
		BuyerName: "นาง ข", Purpose: "ไอ", Pharmacist: "Pharm A",
	}
}

func validKy12() models.Ky12Input {
	return models.Ky12Input{
		Date: "2026-05-17", DrugName: "Methadone", Qty: 10,
		RxNo: "RX-2", PatientName: "นาย ค", Doctor: "Dr B", TotalValue: 250,
	}
}

// --- KY9 -----------------------------------------------------------------

func TestValidateKy9_AcceptsValidInput(t *testing.T) {
	if err := validateKy9Input(validKy9()); err != nil {
		t.Fatalf("valid input should pass: %v", err)
	}
}

func TestValidateKy9_RejectsBlankDate(t *testing.T) {
	in := validKy9()
	in.Date = "   "
	if err := validateKy9Input(in); err == nil || !strings.Contains(err.Error(), "date") {
		t.Fatalf("expected date error, got %v", err)
	}
}

func TestValidateKy9_RejectsBlankDrugName(t *testing.T) {
	in := validKy9()
	in.DrugName = ""
	if err := validateKy9Input(in); err == nil || !strings.Contains(err.Error(), "drug_name") {
		t.Fatalf("expected drug_name error, got %v", err)
	}
}

func TestValidateKy9_RejectsNonPositiveQty(t *testing.T) {
	for _, q := range []int{0, -1, -100} {
		in := validKy9()
		in.Qty = q
		if err := validateKy9Input(in); err == nil || !strings.Contains(err.Error(), "qty") {
			t.Fatalf("expected qty error for %d, got %v", q, err)
		}
	}
}

func TestValidateKy9_RejectsNegativePrice(t *testing.T) {
	in := validKy9()
	in.PricePerUnit = -0.01
	if err := validateKy9Input(in); err == nil || !strings.Contains(err.Error(), "price_per_unit") {
		t.Fatalf("expected price_per_unit error, got %v", err)
	}
}

func TestValidateKy9_AcceptsZeroPrice(t *testing.T) {
	in := validKy9()
	in.PricePerUnit = 0
	if err := validateKy9Input(in); err != nil {
		t.Fatalf("zero price should be allowed (free sample), got %v", err)
	}
}

// --- KY10 ----------------------------------------------------------------

func TestValidateKy10_AcceptsValidInput(t *testing.T) {
	if err := validateKy10Input(validKy10()); err != nil {
		t.Fatalf("valid input should pass: %v", err)
	}
}

func TestValidateKy10_RejectsBlankBuyerName(t *testing.T) {
	in := validKy10()
	in.BuyerName = ""
	if err := validateKy10Input(in); err == nil || !strings.Contains(err.Error(), "buyer_name") {
		t.Fatalf("expected buyer_name error, got %v", err)
	}
}

func TestValidateKy10_RejectsBlankBuyerAddress(t *testing.T) {
	in := validKy10()
	in.BuyerAddress = "   "
	if err := validateKy10Input(in); err == nil || !strings.Contains(err.Error(), "buyer_address") {
		t.Fatalf("expected buyer_address error, got %v", err)
	}
}

func TestValidateKy10_RejectsNonPositiveQty(t *testing.T) {
	in := validKy10()
	in.Qty = 0
	if err := validateKy10Input(in); err == nil {
		t.Fatalf("expected qty error")
	}
}

func TestValidateKy10_RejectsNegativeBalance(t *testing.T) {
	in := validKy10()
	in.Balance = -1
	if err := validateKy10Input(in); err == nil || !strings.Contains(err.Error(), "balance") {
		t.Fatalf("expected balance error, got %v", err)
	}
}

// --- KY11 ----------------------------------------------------------------

func TestValidateKy11_AcceptsValidInput(t *testing.T) {
	if err := validateKy11Input(validKy11()); err != nil {
		t.Fatalf("valid input should pass: %v", err)
	}
}

func TestValidateKy11_RejectsMissingMandatoryFields(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*models.Ky11Input)
		field string
	}{
		{"blank date", func(in *models.Ky11Input) { in.Date = "" }, "date"},
		{"blank drug_name", func(in *models.Ky11Input) { in.DrugName = "" }, "drug_name"},
		{"blank buyer_name", func(in *models.Ky11Input) { in.BuyerName = "" }, "buyer_name"},
		{"blank purpose", func(in *models.Ky11Input) { in.Purpose = "" }, "purpose"},
		{"blank pharmacist", func(in *models.Ky11Input) { in.Pharmacist = "" }, "pharmacist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validKy11()
			c.mut(&in)
			err := validateKy11Input(in)
			if err == nil || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("expected %s error, got %v", c.field, err)
			}
		})
	}
}

// --- KY12 ----------------------------------------------------------------

func TestValidateKy12_AcceptsValidInput(t *testing.T) {
	if err := validateKy12Input(validKy12()); err != nil {
		t.Fatalf("valid input should pass: %v", err)
	}
}

func TestValidateKy12_RejectsMissingRegulatoryFields(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*models.Ky12Input)
		field string
	}{
		{"blank rx_no", func(in *models.Ky12Input) { in.RxNo = "" }, "rx_no"},
		{"blank patient_name", func(in *models.Ky12Input) { in.PatientName = "" }, "patient_name"},
		{"blank doctor", func(in *models.Ky12Input) { in.Doctor = "" }, "doctor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validKy12()
			c.mut(&in)
			err := validateKy12Input(in)
			if err == nil || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("expected %s error, got %v", c.field, err)
			}
		})
	}
}

func TestValidateKy12_RejectsNegativeTotalValue(t *testing.T) {
	in := validKy12()
	in.TotalValue = -0.5
	if err := validateKy12Input(in); err == nil || !strings.Contains(err.Error(), "total_value") {
		t.Fatalf("expected total_value error, got %v", err)
	}
}

func TestValidateKy12_AcceptsZeroTotalValue(t *testing.T) {
	in := validKy12()
	in.TotalValue = 0
	if err := validateKy12Input(in); err != nil {
		t.Fatalf("zero total_value should be allowed (free sample / unrecorded), got %v", err)
	}
}
