package inventory

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

// writtenOffWithReturn writes lot off, then lets goods come back into it the
// way a return or void does (ADR-0007), so it has remaining stock nobody may
// sell.
func (s *store) writtenOffWithReturn(drugID bson.ObjectID, lot models.DrugLot, qty int) {
	s.t.Helper()
	if _, err := WriteOff(s.ctx, s.mdb, []string{lot.ID.Hex()}); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"_id": lot.ID}, bson.M{"$inc": bson.M{"remaining": qty}}); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": drugID}, bson.M{"$inc": bson.M{"stock": qty}}); err != nil {
		s.t.Fatal(err)
	}
}

func TestNextLotIsTheLotASaleWouldTakeFrom(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	early := s.lot(id, "EARLY", 3, 1)
	later := s.lot(id, "LATER", 3, 6)
	s.writtenOffWithReturn(id, early, 2)

	next, err := NextLots(s.ctx, s.mdb)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := next[id]
	if !ok {
		t.Fatal("drug with a sellable lot has no next lot")
	}
	if got.LotID != later.ID {
		t.Fatalf("next lot = %s, want %s (a written-off lot is never sold from)", got.LotNumber, later.LotNumber)
	}
}

func TestNextLotBreaksExpiryTiesTheWaySalesDo(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	expiry := time.Now().AddDate(0, 3, 0).Truncate(time.Millisecond)
	var first models.DrugLot
	for i, n := range []string{"A", "B", "C"} {
		lot := s.lot(id, n, 1, 3)
		if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"_id": lot.ID}, bson.M{"$set": bson.M{"expiry_date": expiry}}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = lot
		}
	}
	next, err := NextLots(s.ctx, s.mdb)
	if err != nil {
		t.Fatal(err)
	}
	if next[id].LotID != first.ID {
		t.Fatalf("next lot = %s, want the first-created lot %s", next[id].LotNumber, first.LotNumber)
	}
}

func TestExpiringLeavesOutWrittenOffLots(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	gone := s.lot(id, "GONE", 3, 1)
	soon := s.lot(id, "SOON", 3, 1)
	s.lot(id, "LATE", 3, 24)
	s.writtenOffWithReturn(id, gone, 2)

	lots, err := Expiring(s.ctx, s.mdb, time.Now(), 60, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lots) != 1 || lots[0].ID != soon.ID {
		t.Fatalf("expiring = %+v, want only %s", lots, soon.LotNumber)
	}
}

func TestLowStockAndTheDashboardCountAgree(t *testing.T) {
	s := newStore(t)
	ins := func(d models.Drug) {
		if _, err := s.mdb.Drugs().InsertOne(s.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	ins(models.Drug{Name: "own-threshold", Stock: 4, MinStock: 5, CostPrice: 1})
	ins(models.Drug{Name: "default-threshold", Stock: 2, CostPrice: 1})
	ins(models.Drug{Name: "plenty", Stock: 50, MinStock: 5, CostPrice: 1})
	ins(models.Drug{Name: "out", Stock: 0, MinStock: 5, CostPrice: 1})
	ins(models.Drug{Name: "oversold", Stock: -2, CostPrice: 1})

	low, err := LowStock(s.ctx, s.mdb, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(low) != 2 || low[0].Name != "default-threshold" || low[1].Name != "own-threshold" {
		t.Fatalf("low stock = %+v", low)
	}
	sum, err := Summarise(s.ctx, s.mdb, 3)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Low != len(low) || sum.Out != 2 || sum.Value != 56 {
		t.Fatalf("summary = %+v, want low %d, out 2, value 56", sum, len(low))
	}
}
