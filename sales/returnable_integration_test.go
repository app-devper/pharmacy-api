package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func (s *shop) lines(saleID string) []models.SaleLine {
	s.t.Helper()
	lines, err := Lines(s.ctx, s.mdb, saleID)
	if err != nil {
		s.t.Fatal(err)
	}
	return lines
}

func (s *shop) returnQty(saleID string, qty int) error {
	_, _, err := Return(s.ctx, s.mdb, saleID, models.DrugReturnInput{
		Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(saleID), Qty: qty}},
	})
	return err
}

// A sale's lines say what can still be returned, by the rule Return enforces.
func TestSaleLinesReportWhatCanStillBeReturned(t *testing.T) {
	s := newShop(t)
	sale := s.sell("sale-1", 4)
	if l := s.lines(sale.ID)[0]; l.ReturnedQty != 0 || l.ReturnableQty != 4 || l.UnlinkedQty != 0 {
		t.Fatalf("fresh line %+v", l)
	}
	if err := s.returnQty(sale.ID, 3); err != nil {
		t.Fatal(err)
	}
	if l := s.lines(sale.ID)[0]; l.ReturnedQty != 3 || l.ReturnableQty != 1 {
		t.Fatalf("after returning 3: %+v", l)
	}
	wantKind(t, s.returnQty(sale.ID, 2), Invalid)
}

// Oversold units have no lot to go back to; asking for them is a 400, not
// a failure.
func TestOversoldUnitsCannotBeReturned(t *testing.T) {
	s := newShop(t)
	in := s.saleInput("sale-1", 12) // 10 in stock
	in.Items[0].AllowOversell = true
	sale, _, err := Sell(s.ctx, s.mdb, in)
	if err != nil {
		t.Fatal(err)
	}
	l := s.lines(sale.ID)[0]
	if l.UnlinkedQty != 2 || l.ReturnableQty != 10 {
		t.Fatalf("oversold line %+v", l)
	}
	wantKind(t, s.returnQty(sale.ID, 11), Invalid)
	if err := s.returnQty(sale.ID, 10); err != nil {
		t.Fatal(err)
	}
}

// A drug without lots returns to stock alone.
func TestALineOfADrugWithoutLotsCanBeReturned(t *testing.T) {
	s := newShop(t)
	if _, err := s.mdb.DrugLots().DeleteMany(s.ctx, bson.M{"drug_id": s.drugID}); err != nil {
		t.Fatal(err)
	}
	sale := s.sell("sale-1", 2)
	if l := s.lines(sale.ID)[0]; l.ReturnableQty != 2 {
		t.Fatalf("lotless line %+v", l)
	}
	if err := s.returnQty(sale.ID, 2); err != nil {
		t.Fatal(err)
	}
	var d models.Drug
	if err := s.mdb.Drugs().FindOne(s.ctx, bson.M{"_id": s.drugID}).Decode(&d); err != nil || d.Stock != 10 {
		t.Fatalf("stock %d (%v), want 10", d.Stock, err)
	}
}
