package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

// repeatingDiscountSale is 2 x 10 with a per-unit discount of 1/3: the
// effective price repeats (9.666...), the line is 19.3333... raw and settles to 19.33.
func (s *shop) repeatingDiscountSale(requestID string, received float64) models.SaleInput {
	return models.SaleInput{
		ClientRequestID: requestID,
		Items:           []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 2, ItemDiscount: 1.0 / 3}},
		Received:        received,
	}
}

func TestRepeatingItemDiscountSettlesToTheSatangAndThePaidTotalIsAccepted(t *testing.T) {
	s := newShop(t)
	resp, _, err := Sell(s.ctx, s.mdb, s.repeatingDiscountSale("", 19.33))
	if err != nil {
		t.Fatalf("sell refused: %v", err)
	}
	if resp.Total != 19.33 || resp.Change != 0 {
		t.Fatalf("total %v change %v, want 19.33 and 0", resp.Total, resp.Change)
	}
	oid, _ := bson.ObjectIDFromHex(resp.ID)
	var item models.SaleItem
	if err := s.mdb.SaleItems().FindOne(s.ctx, bson.M{"sale_id": oid}).Decode(&item); err != nil {
		t.Fatal(err)
	}
	if item.Subtotal != 19.33 {
		t.Fatalf("item subtotal %v, want 19.33", item.Subtotal)
	}
}

func TestBillDiscountWithFractionsOfASatangIsStoredRounded(t *testing.T) {
	s := newShop(t)
	in := models.SaleInput{
		Items:    []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 1}},
		Discount: 1.2345,
		Received: 8.77,
	}
	resp, _, err := Sell(s.ctx, s.mdb, in)
	if err != nil {
		t.Fatalf("sell refused: %v", err)
	}
	if resp.Discount != 1.23 || resp.Total != 8.77 || resp.Change != 0 {
		t.Fatalf("discount %v total %v change %v, want 1.23 8.77 0", resp.Discount, resp.Total, resp.Change)
	}
	oid, _ := bson.ObjectIDFromHex(resp.ID)
	var sale models.Sale
	if err := s.mdb.Sales().FindOne(s.ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		t.Fatal(err)
	}
	if sale.Discount != 1.23 || sale.Total != 8.77 || sale.Received != 8.77 {
		t.Fatalf("stored discount %v total %v received %v", sale.Discount, sale.Total, sale.Received)
	}
}

func TestReceivedOneSatangShortIsStillRefused(t *testing.T) {
	s := newShop(t)
	_, _, err := Sell(s.ctx, s.mdb, s.repeatingDiscountSale("", 19.32))
	wantKind(t, err, Invalid)
}

func TestReplayReturnsTheStoredRoundedAmounts(t *testing.T) {
	s := newShop(t)
	first, _, err := Sell(s.ctx, s.mdb, s.repeatingDiscountSale("req-round", 19.33))
	if err != nil {
		t.Fatal(err)
	}
	again, replayed, err := Sell(s.ctx, s.mdb, s.repeatingDiscountSale("req-round", 19.33))
	if err != nil || !replayed {
		t.Fatalf("expected replay, got replayed=%v err=%v", replayed, err)
	}
	if again.Total != 19.33 || again.Discount != first.Discount {
		t.Fatalf("replay total %v discount %v, first discount %v", again.Total, again.Discount, first.Discount)
	}
}
