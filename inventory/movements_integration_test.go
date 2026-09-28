package inventory

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func (s *store) movements(drugID bson.ObjectID) []Movement {
	s.t.Helper()
	moves, err := Movements(s.ctx, s.mdb, MovementQuery{
		From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Kinds: AllMoves,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	var out []Movement
	for _, m := range moves {
		if m.DrugID == drugID.Hex() {
			out = append(out, m)
		}
	}
	return out
}

func (s *store) adjust(drugID bson.ObjectID, delta int, lot *models.LotTarget) {
	s.t.Helper()
	if _, err := Adjust(s.ctx, s.mdb, drugID.Hex(), models.StockAdjustmentInput{Delta: delta, Reason: "อื่นๆ", Lot: lot}); err != nil {
		s.t.Fatal(err)
	}
}

func sum(moves []Movement) (total int) {
	for _, m := range moves {
		total += m.Delta
	}
	return total
}

func count(moves []Movement, kind string) (n int) {
	for _, m := range moves {
		if m.Type == kind {
			n++
		}
	}
	return n
}

// Every inventory command shows once, so a drug's movements add up to how
// its stock changed.
func TestMovementsAddUpToTheStockChange(t *testing.T) {
	s := newStore(t)
	drugID := s.drug(0)
	next := time.Now().AddDate(1, 0, 0).Format(dateLayout)

	l1 := s.lot(drugID, "L1", 10, 12)                                          // import +10
	s.adjust(drugID, 3, &models.LotTarget{LotNumber: "L2", ExpiryDate: next})  // new lot: adjustment +3 only
	s.adjust(drugID, 2, &models.LotTarget{LotID: l1.ID.Hex()})                 // existing lot: adjustment +2
	s.adjust(drugID, -4, nil)                                                  // adjustment −4
	l3 := s.lot(drugID, "L3", 5, 6)                                            // import +5 …
	if err := DeleteLot(s.ctx, s.mdb, drugID.Hex(), l3.ID.Hex()); err != nil { // … undone: neither shows
		t.Fatal(err)
	}
	s.adjust(drugID, 4, &models.LotTarget{LotNumber: "L4", ExpiryDate: next}) // adjustment +4 …
	var l4 models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"lot_number": "L4"}).Decode(&l4); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLot(s.ctx, s.mdb, drugID.Hex(), l4.ID.Hex()); err != nil { // … deleted: writeoff −4
		t.Fatal(err)
	}
	if _, err := WriteOff(s.ctx, s.mdb, []string{l1.ID.Hex()}); err != nil { // writeoff −remaining
		t.Fatal(err)
	}

	moves := s.movements(drugID)
	if got, want := sum(moves), s.stock(drugID); got != want {
		t.Fatalf("movements add up to %d, stock is %d: %+v", got, want, moves)
	}
	if n := count(moves, MoveImport); n != 1 {
		t.Fatalf("%d imports, want only L1: %+v", n, moves)
	}
	if n := count(moves, MoveWriteoff); n != 2 {
		t.Fatalf("%d writeoffs, want L4's deletion and L1: %+v", n, moves)
	}
	s.consistent()
}

// Lots created by an increase before origins were recorded are recognised
// by the adjustment written with them.
func TestLegacyLotsFromAnIncreaseAreNotImports(t *testing.T) {
	s := newStore(t)
	drugID := s.drug(0)
	s.lot(drugID, "L1", 10, 12)
	s.adjust(drugID, 3, &models.LotTarget{LotNumber: "L2", ExpiryDate: time.Now().AddDate(1, 0, 0).Format(dateLayout)})
	if _, err := s.mdb.DrugLots().UpdateMany(s.ctx, bson.M{"drug_id": drugID}, bson.M{"$unset": bson.M{"origin": ""}}); err != nil {
		t.Fatal(err)
	}
	moves := s.movements(drugID)
	if count(moves, MoveImport) != 1 || sum(moves) != 13 {
		t.Fatalf("want one import and a total of 13: %+v", moves)
	}
}

func TestOpeningStockIsOneImport(t *testing.T) {
	s := newStore(t)
	drug := models.Drug{Name: "Opened", CostPrice: 1, Stock: 7}
	err := s.mdb.WithTransaction(s.ctx, func(txCtx context.Context) error {
		res, err := s.mdb.Drugs().InsertOne(txCtx, drug)
		if err != nil {
			return err
		}
		drug.ID = res.InsertedID.(bson.ObjectID)
		return OpenStock(txCtx, s.mdb, OpeningLot(drug))
	})
	if err != nil {
		t.Fatal(err)
	}
	moves := s.movements(drug.ID)
	if len(moves) != 1 || moves[0].Type != MoveImport || moves[0].Delta != 7 {
		t.Fatalf("want one import of 7: %+v", moves)
	}
	s.consistent()
}
