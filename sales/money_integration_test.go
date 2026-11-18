package sales

import (
	"fmt"
	"testing"
	"time"

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

func TestReturnRefundIsSettledToTheSatang(t *testing.T) {
	s := newShop(t)
	sale, _, err := Sell(s.ctx, s.mdb, models.SaleInput{
		Items:    []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 6}},
		Discount: 0.5,
		Received: 100,
	})
	if err != nil {
		t.Fatalf("sell refused: %v", err)
	}
	ret, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		ClientRequestID: "req-refund-round",
		Reason:          "r",
		Items:           []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 1}},
	})
	if err != nil {
		t.Fatalf("return refused: %v", err)
	}
	if ret.Refund != 9.92 || ret.Items[0].Subtotal != 9.92 {
		t.Fatalf("refund %v line subtotal %v, want 9.92 and 9.92", ret.Refund, ret.Items[0].Subtotal)
	}
}

// addDrug stocks one more drug @ 10 with a single lot of 10 and returns its id.
func (s *shop) addDrug(name string) bson.ObjectID {
	s.t.Helper()
	res, err := s.mdb.Drugs().InsertOne(s.ctx, models.Drug{Name: name, SellPrice: 10, CostPrice: 4, Stock: 10})
	if err != nil {
		s.t.Fatal(err)
	}
	id := res.InsertedID.(bson.ObjectID)
	if _, err := s.mdb.DrugLots().InsertOne(s.ctx, models.DrugLot{
		DrugID: id, LotNumber: "L1", ExpiryDate: time.Now().AddDate(1, 0, 0), Quantity: 10, Remaining: 10,
	}); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func TestFullReturnRefundsExactlyWhatWasPaid(t *testing.T) {
	s := newShop(t)
	drugs := []bson.ObjectID{s.drugID, s.addDrug("B"), s.addDrug("C")}
	items := make([]models.SaleItemInput, 0, len(drugs))
	for _, d := range drugs {
		items = append(items, models.SaleItemInput{DrugID: d.Hex(), Qty: 1})
	}
	sale, _, err := Sell(s.ctx, s.mdb, models.SaleInput{Items: items, Discount: 0.01, Received: 100})
	if err != nil {
		t.Fatalf("sell refused: %v", err)
	}
	if sale.Total != 29.99 {
		t.Fatalf("total %v, want 29.99", sale.Total)
	}
	oid, _ := bson.ObjectIDFromHex(sale.ID)
	cur, err := s.mdb.SaleItems().Find(s.ctx, bson.M{"sale_id": oid})
	if err != nil {
		t.Fatal(err)
	}
	var lines []models.SaleItem
	if err := cur.All(s.ctx, &lines); err != nil {
		t.Fatal(err)
	}
	back := make([]models.ReturnItemInput, 0, len(lines))
	for _, l := range lines {
		back = append(back, models.ReturnItemInput{SaleItemID: l.ID.Hex(), Qty: 1})
	}
	ret, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{Reason: "r", Items: back})
	if err != nil {
		t.Fatalf("return refused: %v", err)
	}
	if ret.Refund != 29.99 {
		t.Fatalf("refund %v, want 29.99", ret.Refund)
	}
}

func TestRepeatedPartialReturnsNeverRefundMoreThanPaid(t *testing.T) {
	s := newShop(t)
	sale, _, err := Sell(s.ctx, s.mdb, models.SaleInput{
		Items:    []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 6}},
		Discount: 0.5,
		Received: 100,
	})
	if err != nil {
		t.Fatalf("sell refused: %v", err)
	}
	if sale.Total != 59.5 {
		t.Fatalf("total %v, want 59.5", sale.Total)
	}
	itemID := s.saleItemID(sale.ID)
	sum := 0.0
	last := 0.0
	for i := 0; i < 6; i++ {
		ret, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
			ClientRequestID: fmt.Sprintf("req-part-%d", i),
			Reason:          "r",
			Items:           []models.ReturnItemInput{{SaleItemID: itemID, Qty: 1}},
		})
		if err != nil {
			t.Fatalf("return %d refused: %v", i, err)
		}
		if i < 5 {
			sum = satang(sum + ret.Refund)
		}
		last = ret.Refund
	}
	if want := satang(59.5 - sum); last != want {
		t.Fatalf("last refund %v, want %v", last, want)
	}
	if total := satang(sum + last); total != 59.5 {
		t.Fatalf("refunded %v in total, want 59.5", total)
	}
}
