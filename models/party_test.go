package models

import (
	"errors"
	"testing"
)

func TestCustomerAcceptStoresTheTrimmedValuesItChecked(t *testing.T) {
	got, err := CustomerInput{Name: "  สมชาย ", Phone: " 081 ", PriceTier: " wholesale ", Disease: "  "}.Accept()
	if err != nil {
		t.Fatal(err)
	}
	want := CustomerInput{Name: "สมชาย", Phone: "081", PriceTier: "wholesale", Disease: "-"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCustomerAcceptRefusesABlankNameOrABadTier(t *testing.T) {
	var refusal InputError
	for _, in := range []CustomerInput{
		{Name: "   "},
		{Name: "A", PriceTier: "this tier name is far longer than thirty-two characters"},
	} {
		if _, err := in.Accept(); !errors.As(err, &refusal) {
			t.Fatalf("%+v: want an InputError, got %v", in, err)
		}
	}
}

func TestSupplierAcceptTrimsSoTheUniqueNameHolds(t *testing.T) {
	got, err := SupplierInput{Name: "ABC ", ContactName: " Ann", Phone: " 02 ", Address: " x ", TaxID: " 1 ", Notes: " n "}.Accept()
	if err != nil {
		t.Fatal(err)
	}
	want := SupplierInput{Name: "ABC", ContactName: "Ann", Phone: "02", Address: "x", TaxID: "1", Notes: "n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := (SupplierInput{Name: " "}).Accept(); err == nil {
		t.Fatal("a blank supplier name must be refused")
	}
}
