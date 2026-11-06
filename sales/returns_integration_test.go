package sales

import (
	"testing"

	"pharmacy-pos/backend/models"
)

func TestReturnsListsASalesReturnsNewestFirst(t *testing.T) {
	// Arrange
	s := newShop(t)
	sale := s.sell("sale-1", 5)
	item := s.saleItemID(sale.ID)
	for i, qty := range []int{1, 2} {
		_, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
			ClientRequestID: []string{"ret-1", "ret-2"}[i],
			Reason:          "r",
			Items:           []models.ReturnItemInput{{SaleItemID: item, Qty: qty}},
		})
		if err != nil {
			t.Fatalf("return qty %d: %v", qty, err)
		}
	}

	// Act
	got, err := Returns(s.ctx, s.mdb, sale.ID)

	// Assert
	if err != nil || len(got) != 2 || got[0].Items[0].Qty != 2 {
		t.Fatalf("want 2 returns newest (qty 2) first, got %+v err=%v", got, err)
	}

	none, err := Returns(s.ctx, s.mdb, "64b64b64b64b64b64b64b64b")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("unknown sale: want empty non-nil slice, got %#v err=%v", none, err)
	}

	_, err = Returns(s.ctx, s.mdb, "zz")
	wantKind(t, err, Invalid)
}
