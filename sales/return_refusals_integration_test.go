package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

// A Return after a completed void is refused with the Invalid kind and the
// Thai message the shop sees; stock is untouched by the refused Return.
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
	wantKind(t, err, Invalid)
	if err.Error() != msgReturnOfVoidedSale {
		t.Fatalf("message = %q, want %q", err.Error(), msgReturnOfVoidedSale)
	}
	if drug, lot := s.stock(); drug != 10 || lot != 10 {
		t.Fatalf("stock drug=%d lot=%d, want 10/10", drug, lot)
	}
}

func TestReturnOfAnUnknownSaleIsNotFound(t *testing.T) {
	s := newShop(t)
	other := s.sell("unknown-other", 4)
	_, _, err := Return(s.ctx, s.mdb, bson.NewObjectID().Hex(), models.DrugReturnInput{
		ClientRequestID: "unknown-ret", Reason: "r",
		Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(other.ID), Qty: 1}},
	})
	wantKind(t, err, NotFound)
	if drug, lot := s.stock(); drug != 6 || lot != 6 {
		t.Fatalf("stock drug=%d lot=%d, want 6/6", drug, lot)
	}
}

func TestReturnOfAMalformedSaleItemIdIsRefused(t *testing.T) {
	s := newShop(t)
	sale := s.sell("malformed-sale", 4)
	_, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		ClientRequestID: "malformed-ret", Reason: "r",
		Items: []models.ReturnItemInput{{SaleItemID: "not-hex", Qty: 1}},
	})
	wantKind(t, err, Invalid)
}
