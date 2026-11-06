package sales

import (
	"testing"

	"pharmacy-pos/backend/models"
)

// Sequential control for the race probe: a Return after a completed void must
// be refused. If this passes while the race test fails, the hole is the window
// between Return's pre-check and its transaction, not a missing check.
func TestReturnAfterACompletedVoidIsRefused(t *testing.T) {
	s := newShop(t)
	sale := s.sell("seq-sale", 4)
	if err := Void(s.ctx, s.mdb, sale.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	_, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		ClientRequestID: "seq-ret", Reason: "r",
		Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 4}},
	})
	if err == nil {
		t.Fatal("return after void was accepted")
	}
	if drug, lot := s.stock(); drug != 10 || lot != 10 {
		t.Fatalf("stock drug=%d lot=%d, want 10/10", drug, lot)
	}
}
