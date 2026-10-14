package catalog

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

func isRefusal(err error) bool {
	var r *refusal.Error
	return errors.As(err, &r) && r.Kind == refusal.Invalid
}

func TestReportTypesAcceptsTheRegisterInAnySpellingAndRefusesTypos(t *testing.T) {
	got, err := ReportTypes([]string{"ky10", " KY 11 ", "ขย.12", "ขย 9", "ky10", ""})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ky10", "ky11", "ky12", "ky9"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"ky13", "controlled", "10x"} {
		if _, err := ReportTypes([]string{bad}); !isRefusal(err) {
			t.Fatalf("%q: want a refusal, got %v", bad, err)
		}
	}
	if got, _ := ReportTypes(nil); got == nil || len(got) != 0 {
		t.Fatalf("no types must be an empty list, got %#v", got)
	}
}

func TestEditNeedsANameAndDefaultsTheUnitLikeACreate(t *testing.T) {
	if _, err := Edit(models.DrugUpdate{Name: "  "}); !isRefusal(err) {
		t.Fatalf("an unnamed edit must be refused, got %v", err)
	}
	got, err := Edit(models.DrugUpdate{Name: " Para ", SellPrice: 10, ReportTypes: []string{"ขย.11"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Para" || got.Unit != DefaultUnit || got.Type != DefaultType || got.Prices[models.TierRetail] != 10 || !reflect.DeepEqual(got.ReportTypes, []string{"ky11"}) {
		t.Fatalf("unexpected edit %+v", got)
	}
}

func TestAltUnitsAreCheckedAgainstTheDefaultedBaseUnit(t *testing.T) {
	_, err := Edit(models.DrugUpdate{Name: "Para", AltUnits: []models.AltUnit{{Name: DefaultUnit, Factor: 10}}})
	if !isRefusal(err) {
		t.Fatalf("an alt unit named like the base unit must be refused, got %v", err)
	}
}

func TestImportedDrugIsPermissiveAboutStockButNotAboutTheRest(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	d, err := ImportedDrug(models.DrugInput{Name: "Para", Stock: -5, SellPrice: 12, ReportTypes: []string{"KY10"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Stock != 0 || d.Unit != DefaultUnit || d.Prices[models.TierRetail] != 12 || !reflect.DeepEqual(d.ReportTypes, []string{"ky10"}) {
		t.Fatalf("unexpected row %+v", d)
	}
	for name, in := range map[string]models.DrugInput{
		"negative price": {Name: "Para", SellPrice: -1},
		"negative cost":  {Name: "Para", CostPrice: -1},
		"typo register":  {Name: "Para", ReportTypes: []string{"ky 13"}},
	} {
		if _, err := ImportedDrug(in, now); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}

func TestLinePriceSellsTheDrugAsItWasSaved(t *testing.T) {
	drug := models.Drug{
		Name: "Para", SellPrice: 10, Prices: models.PriceTiers{"retail": 10, "wholesale": 8},
		AltUnits: []models.AltUnit{{Name: "กล่อง", Factor: 3, SellPrice: 100, Prices: models.PriceTiers{"retail": 100, "wholesale": 90}}},
	}
	cases := []struct {
		name       string
		unit       string
		factor     int
		qty        int
		tier       string
		wantPrice  float64
		wantFactor int
		wantErr    bool
	}{
		{"base retail", "", 0, 2, "", 10, 1, false},
		{"base wholesale", "", 0, 2, "wholesale", 8, 1, false},
		{"unset tier falls back to retail", "", 0, 2, "regular", 10, 1, false},
		{"alt unit per base, rounded", "กล่อง", 0, 6, "", 33.33, 3, false},
		{"alt unit at a tier", "กล่อง", 3, 6, "wholesale", 30, 3, false},
		{"unknown unit", "แผง", 0, 6, "", 0, 0, true},
		{"wrong factor", "กล่อง", 2, 6, "", 0, 0, true},
		{"partial box", "กล่อง", 0, 4, "", 0, 0, true},
		{"bad tier", "", 0, 1, "a\x01", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price, factor, err := LinePrice(drug, c.unit, c.factor, c.qty, c.tier)
			if c.wantErr {
				if !isRefusal(err) {
					t.Fatalf("want a refusal, got %v", err)
				}
				return
			}
			if err != nil || price != c.wantPrice || factor != c.wantFactor {
				t.Fatalf("LinePrice = %v, %v, %v; want %v, %v", price, factor, err, c.wantPrice, c.wantFactor)
			}
		})
	}
}
