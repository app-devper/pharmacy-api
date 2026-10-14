package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func (s *shop) customer() bson.ObjectID {
	s.t.Helper()
	res, err := s.mdb.Customers().InsertOne(s.ctx, models.Customer{Name: "คุณ ก"})
	if err != nil {
		s.t.Fatal(err)
	}
	return res.InsertedID.(bson.ObjectID)
}

func (s *shop) spent(id bson.ObjectID) (float64, bool) {
	s.t.Helper()
	var c models.Customer
	if err := s.mdb.Customers().FindOne(s.ctx, bson.M{"_id": id}).Decode(&c); err != nil {
		s.t.Fatal(err)
	}
	return c.TotalSpent, c.LastVisit != nil
}

func TestACustomersSpendFollowsTheSaleItsReturnAndItsVoid(t *testing.T) {
	s := newShop(t)
	id := s.customer()
	in := s.saleInput("spend-1", 5)
	hex := id.Hex()
	in.CustomerID = &hex
	sale, _, err := Sell(s.ctx, s.mdb, in)
	if err != nil {
		t.Fatal(err)
	}
	if spent, visited := s.spent(id); spent != 50 || !visited {
		t.Fatalf("after the sale: spent %v visited %v, want 50 true", spent, visited)
	}
	if err := s.returnQty(sale.ID, 2); err != nil {
		t.Fatal(err)
	}
	if spent, _ := s.spent(id); spent != 30 {
		t.Fatalf("after returning 2: spent %v, want 30", spent)
	}
	if err := Void(s.ctx, s.mdb, sale.ID, "ลูกค้ายกเลิก"); err != nil {
		t.Fatal(err)
	}
	if spent, _ := s.spent(id); spent != 0 {
		t.Fatalf("after the void: spent %v, want 0", spent)
	}

	sales, err := CustomerSales(s.ctx, s.mdb, id, 100)
	if err != nil || len(sales) != 1 || sales[0].ID.Hex() != sale.ID {
		t.Fatalf("customer's sales = %v, %v", sales, err)
	}
}
