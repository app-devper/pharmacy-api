package handlers

import (
	"testing"
	"time"

	"pharmacy-pos/backend/models"
)

var kyTestTime = time.Date(2026, 5, 17, 9, 30, 0, 0, time.UTC)

func TestBuildKy9PayloadTrimsSaleIDAndComputesTotal(t *testing.T) {
	doc := buildKy9Payload(models.Ky9Input{
		SaleID:       "  abc123  ",
		Date:         "2026-05-17",
		DrugName:     "Paracetamol",
		Qty:          4,
		PricePerUnit: 12.5,
	}, kyTestTime)

	if doc.SaleID != "abc123" {
		t.Fatalf("expected SaleID to be trimmed to %q, got %q", "abc123", doc.SaleID)
	}
	if doc.TotalValue != 50.0 {
		t.Fatalf("expected TotalValue 50.0, got %v", doc.TotalValue)
	}
	if !doc.CreatedAt.Equal(kyTestTime) {
		t.Fatalf("expected CreatedAt %v, got %v", kyTestTime, doc.CreatedAt)
	}
}

func TestBuildKy9PayloadEmptySaleIDStaysEmpty(t *testing.T) {
	doc := buildKy9Payload(models.Ky9Input{Qty: 1, PricePerUnit: 10}, kyTestTime)
	if doc.SaleID != "" {
		t.Fatalf("expected empty SaleID, got %q", doc.SaleID)
	}
	if doc.TotalValue != 10 {
		t.Fatalf("expected TotalValue 10, got %v", doc.TotalValue)
	}
}

func TestBuildKy10PayloadCopiesSaleIDAndBuyerFields(t *testing.T) {
	doc := buildKy10Payload(models.Ky10Input{
		SaleID:       "sale-xyz",
		Date:         "2026-05-17",
		DrugName:     "Morphine",
		BuyerName:    "สมชาย",
		BuyerAddress: "กรุงเทพฯ",
		RxNo:         "RX-001",
		Doctor:       "นพ.สมศักดิ์",
		Qty:          2,
		Balance:      10,
	}, kyTestTime)

	if doc.SaleID != "sale-xyz" {
		t.Fatalf("expected SaleID sale-xyz, got %q", doc.SaleID)
	}
	if doc.BuyerName != "สมชาย" {
		t.Fatalf("expected BuyerName สมชาย, got %q", doc.BuyerName)
	}
	if doc.Balance != 10 {
		t.Fatalf("expected Balance 10, got %d", doc.Balance)
	}
}

func TestBuildKy11PayloadCarriesSaleIDAndPharmacist(t *testing.T) {
	doc := buildKy11Payload(models.Ky11Input{
		SaleID:     "  sale-abc  ",
		Pharmacist: "ภญ.จิราภรณ์",
		Purpose:    "บรรเทาอาการปวด",
		Qty:        3,
	}, kyTestTime)

	if doc.SaleID != "sale-abc" {
		t.Fatalf("expected trimmed SaleID, got %q", doc.SaleID)
	}
	if doc.Pharmacist != "ภญ.จิราภรณ์" {
		t.Fatalf("expected pharmacist preserved, got %q", doc.Pharmacist)
	}
}

func TestBuildKy12PayloadDefaultsStatusWhenBlank(t *testing.T) {
	doc := buildKy12Payload(models.Ky12Input{
		SaleID:      "sale-789",
		PatientName: "ผู้ป่วย A",
		Status:      "",
	}, kyTestTime)

	if doc.SaleID != "sale-789" {
		t.Fatalf("expected SaleID sale-789, got %q", doc.SaleID)
	}
	if doc.Status != "จ่ายแล้ว" {
		t.Fatalf("expected default status จ่ายแล้ว, got %q", doc.Status)
	}
}

func TestBuildKy12PayloadPreservesExplicitStatus(t *testing.T) {
	doc := buildKy12Payload(models.Ky12Input{
		Status: "รอจ่าย",
	}, kyTestTime)

	if doc.Status != "รอจ่าย" {
		t.Fatalf("expected status รอจ่าย, got %q", doc.Status)
	}
}
