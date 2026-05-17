package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pharmacy-pos/backend/models"
)

func postLabels(t *testing.T, h *LabelHandler, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/labels/print", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.Print(rec, req)
	return rec
}

func TestPrintLabelsRejectsEmptyLines(t *testing.T) {
	h := NewLabelHandler()
	rec := postLabels(t, h, models.PrintLabelsInput{Size: "38x25", Lines: nil})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPrintLabelsRejectsZeroTotalCopies(t *testing.T) {
	h := NewLabelHandler()
	rec := postLabels(t, h, models.PrintLabelsInput{
		Size: "38x25",
		Lines: []models.LabelLineInput{
			{DrugName: "Paracetamol", Barcode: "8851234567001", Copies: 0},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPrintLabelsRejectsNegativeCopies(t *testing.T) {
	h := NewLabelHandler()
	rec := postLabels(t, h, models.PrintLabelsInput{
		Size: "38x25",
		Lines: []models.LabelLineInput{
			{DrugName: "X", Barcode: "8851234567001", Copies: -5},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPrintLabelsHappyPathReturnsPdf(t *testing.T) {
	h := NewLabelHandler()
	rec := postLabels(t, h, models.PrintLabelsInput{
		Size: "38x25",
		Lines: []models.LabelLineInput{
			{DrugName: "พาราเซตามอล 500mg", Barcode: "8851234567001", Price: 2.0, IncludePrice: true, Copies: 6},
			{DrugName: "Amoxicillin 500mg", Barcode: "8851234567002", Copies: 3},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("expected pdf content-type, got %q", got)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment; filename=") {
		t.Fatalf("expected attachment disposition, got %q", disposition)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("response is not a PDF (first 10 bytes: %q)", rec.Body.Bytes()[:10])
	}
}

func TestPrintLabelsRejectsTooManyLines(t *testing.T) {
	h := NewLabelHandler()
	lines := make([]models.LabelLineInput, maxLabelRequestLines+1)
	for i := range lines {
		lines[i] = models.LabelLineInput{DrugName: "X", Barcode: "12345", Copies: 1}
	}
	rec := postLabels(t, h, models.PrintLabelsInput{Size: "38x25", Lines: lines})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestLabelSizeFromWire(t *testing.T) {
	if got := models.LabelSizeFromWire(""); got != models.LabelSizeSmall {
		t.Fatalf("expected default to be small (38x25), got %+v", got)
	}
	if got := models.LabelSizeFromWire("38x25"); got != models.LabelSizeSmall {
		t.Fatalf("expected small for 38x25, got %+v", got)
	}
	if got := models.LabelSizeFromWire("50x30"); got != models.LabelSizeMedium {
		t.Fatalf("expected medium for 50x30, got %+v", got)
	}
	if got := models.LabelSizeFromWire("medium"); got != models.LabelSizeMedium {
		t.Fatalf("expected medium for 'medium', got %+v", got)
	}
}
