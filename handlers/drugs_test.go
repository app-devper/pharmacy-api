package handlers

import (
	"testing"
	"time"

	"pharmacy-pos/backend/models"
)

func TestBuildDrugCreatePayloadWithoutLotRejectsPositiveStock(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.Local)
	_, _, err := buildDrugCreatePayload(models.DrugInput{
		Name:      "Paracetamol",
		Stock:     12,
		SellPrice: 20,
		CostPrice: 10,
	}, now)
	if err == nil || err.Error() != "create_lot is required when stock > 0" {
		t.Fatalf("expected create_lot validation error, got %v", err)
	}
}

func TestBuildDrugCreatePayloadWithLotSetsStockFromLot(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.Local)
	cost := 8.5
	sell := 15.0
	drug, lot, err := buildDrugCreatePayload(models.DrugInput{
		Name:      "Amoxicillin",
		Stock:     0,
		SellPrice: 15,
		CostPrice: 8,
		CreateLot: &models.DrugLotInput{
			LotNumber:  "LOT-001",
			ExpiryDate: "2027-12-31",
			ImportDate: "2026-04-18",
			Quantity:   25,
			CostPrice:  &cost,
			SellPrice:  &sell,
		},
	}, now)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if lot == nil {
		t.Fatal("expected lot to be created")
	}
	if drug.Stock != 25 {
		t.Fatalf("expected stock 25, got %d", drug.Stock)
	}
	if lot.Quantity != 25 || lot.Remaining != 25 {
		t.Fatalf("expected lot quantity/remaining 25, got %d/%d", lot.Quantity, lot.Remaining)
	}
	if got := lot.ImportDate.Format("2006-01-02"); got != "2026-04-18" {
		t.Fatalf("expected import date 2026-04-18, got %s", got)
	}
	if lot.CostPrice == nil || *lot.CostPrice != cost {
		t.Fatalf("expected lot cost price %.2f, got %+v", cost, lot.CostPrice)
	}
	if lot.SellPrice == nil || *lot.SellPrice != sell {
		t.Fatalf("expected lot sell price %.2f, got %+v", sell, lot.SellPrice)
	}
}

func TestBuildDrugCreatePayloadWithLotAllowsMatchingStock(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.Local)
	drug, lot, err := buildDrugCreatePayload(models.DrugInput{
		Name:  "Ibuprofen",
		Stock: 10,
		CreateLot: &models.DrugLotInput{
			LotNumber:  "LOT-002",
			ExpiryDate: "2027-01-01",
			Quantity:   10,
		},
	}, now)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if lot == nil {
		t.Fatal("expected lot to be created")
	}
	if drug.Stock != 10 {
		t.Fatalf("expected stock 10, got %d", drug.Stock)
	}
}

func TestBuildDrugCreatePayloadWithLotRejectsMismatchedStock(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.Local)
	_, _, err := buildDrugCreatePayload(models.DrugInput{
		Name:  "Cetirizine",
		Stock: 5,
		CreateLot: &models.DrugLotInput{
			LotNumber:  "LOT-003",
			ExpiryDate: "2027-01-01",
			Quantity:   10,
		},
	}, now)
	if err == nil {
		t.Fatal("expected error for mismatched stock")
	}
	want := "stock must be 0 or equal create_lot.quantity when create_lot is provided"
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}

func TestBuildDrugCreatePayloadWithLotRejectsBadDates(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 0, 0, 0, time.Local)
	_, _, err := buildDrugCreatePayload(models.DrugInput{
		Name: "Loratadine",
		CreateLot: &models.DrugLotInput{
			LotNumber:  "LOT-004",
			ExpiryDate: "bad-date",
			Quantity:   10,
		},
	}, now)
	if err == nil || err.Error() != "create_lot.expiry_date must be YYYY-MM-DD" {
		t.Fatalf("expected expiry validation error, got %v", err)
	}

	_, _, err = buildDrugCreatePayload(models.DrugInput{
		Name: "Loratadine",
		CreateLot: &models.DrugLotInput{
			LotNumber:  "LOT-005",
			ExpiryDate: "2027-01-01",
			ImportDate: "bad-date",
			Quantity:   10,
		},
	}, now)
	if err == nil || err.Error() != "create_lot.import_date must be YYYY-MM-DD" {
		t.Fatalf("expected import-date validation error, got %v", err)
	}
}
