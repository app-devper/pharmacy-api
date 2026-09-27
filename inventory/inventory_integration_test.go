package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// Integration tests against a MongoDB replica set (MONGO_TEST_URI), like the
// sales package: transactions and the stock invariant are the point.

var testMgr *db.Manager

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("inventory: MONGO_TEST_URI not set; skipping integration tests")
		os.Exit(0)
	}
	prefix := fmt.Sprintf("inventory_test_%d", time.Now().UnixNano())
	testMgr = db.NewManager(uri, prefix)
	code := m.Run()
	if client, err := mongo.Connect(options.Client().ApplyURI(uri)); err == nil {
		ctx := context.Background()
		names, _ := client.ListDatabaseNames(ctx, bson.M{"name": bson.M{"$regex": "^" + prefix}})
		for _, n := range names {
			_ = client.Database(n).Drop(ctx)
		}
		_ = client.Disconnect(ctx)
	}
	os.Exit(code)
}

var (
	tenantMu  sync.Mutex
	tenantSeq int
)

type store struct {
	t   *testing.T
	ctx context.Context
	mdb *db.MongoDB
}

func newStore(t *testing.T) *store {
	t.Helper()
	tenantMu.Lock()
	tenantSeq++
	client := fmt.Sprintf("s%d", tenantSeq)
	tenantMu.Unlock()
	mdb, err := testMgr.ForClient(client)
	if err != nil {
		t.Fatal(err)
	}
	return &store{t: t, ctx: context.Background(), mdb: mdb}
}

func (s *store) drug(stock int) bson.ObjectID {
	s.t.Helper()
	res, err := s.mdb.Drugs().InsertOne(s.ctx, models.Drug{Name: "Amoxicillin", CostPrice: 5, SellPrice: 10, Stock: stock})
	if err != nil {
		s.t.Fatal(err)
	}
	return res.InsertedID.(bson.ObjectID)
}

// lot adds a lot the way goods arrive (stock and lot together).
func (s *store) lot(drugID bson.ObjectID, number string, qty, monthsToExpiry int) models.DrugLot {
	s.t.Helper()
	var lot models.DrugLot
	err := s.mdb.WithTransaction(s.ctx, func(txCtx context.Context) error {
		var err error
		lot, err = ReceiveLot(txCtx, s.mdb, models.DrugLot{
			DrugID: drugID, LotNumber: number, Quantity: qty, ExpiryDate: time.Now().AddDate(0, monthsToExpiry, 0),
		})
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return lot
}

func (s *store) remaining(lotID bson.ObjectID) int {
	s.t.Helper()
	var l models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"_id": lotID}).Decode(&l); err != nil {
		s.t.Fatal(err)
	}
	return l.Remaining
}

func (s *store) stock(drugID bson.ObjectID) int {
	s.t.Helper()
	d, err := loadDrug(s.ctx, s.mdb, drugID)
	if err != nil {
		s.t.Fatal(err)
	}
	return d.Stock
}

// consistent checks the ADR-0007 invariant for every lot-tracked drug.
func (s *store) consistent() {
	s.t.Helper()
	rows, err := Drift(s.ctx, s.mdb)
	if err != nil {
		s.t.Fatal(err)
	}
	if len(rows) > 0 {
		s.t.Fatalf("stock and lots disagree: %+v", rows)
	}
}

// oversell records a sale line of qty that may exceed the lots.
func (s *store) oversell(drugID bson.ObjectID, qty int) {
	s.t.Helper()
	d, _ := loadDrug(s.ctx, s.mdb, drugID)
	err := s.mdb.WithTransaction(s.ctx, func(txCtx context.Context) error {
		taken, err := Take(txCtx, s.mdb, d, qty, true)
		if err != nil {
			return err
		}
		_, err = s.mdb.SaleItems().InsertOne(txCtx, models.SaleItem{DrugID: drugID, DrugName: d.Name, Qty: qty, LotSplits: taken.Splits, OversoldQty: taken.Oversold})
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

func wantKind(t *testing.T, err error, kind refusal.Kind) {
	t.Helper()
	var e *refusal.Error
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("expected refusal kind %d, got %v", kind, err)
	}
}

func TestAddLotSettlesOversoldStock(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	first := s.lot(id, "L0", 2, 6)
	s.oversell(id, 5) // takes the 2 in L0, 3 oversold
	if got := s.stock(id); got != -3 {
		t.Fatalf("stock %d, want -3", got)
	}
	s.consistent()

	lot, err := AddLot(s.ctx, s.mdb, id.Hex(), models.DrugLotInput{LotNumber: "L1", Quantity: 10, ExpiryDate: time.Now().AddDate(1, 0, 0).Format(dateLayout)})
	if err != nil {
		t.Fatal(err)
	}
	if s.remaining(lot.ID) != 7 || s.stock(id) != 7 || s.remaining(first.ID) != 0 {
		t.Fatalf("after receiving 10: lot %d stock %d, want 7 and 7", s.remaining(lot.ID), s.stock(id))
	}
	s.consistent()
}

func TestDecreaseTakesFromLotsFirstExpiryFirst(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	soon := s.lot(id, "SOON", 2, 1)
	late := s.lot(id, "LATE", 5, 12)
	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: -3, Reason: "ยาเสียหาย"}); err != nil {
		t.Fatal(err)
	}
	if s.remaining(soon.ID) != 0 || s.remaining(late.ID) != 4 || s.stock(id) != 4 {
		t.Fatalf("soon %d late %d stock %d", s.remaining(soon.ID), s.remaining(late.ID), s.stock(id))
	}
	s.consistent()
}

func TestIncreaseGoesIntoTheNamedOrAssumedLot(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	a := s.lot(id, "A", 3, 3)
	b := s.lot(id, "B", 3, 9)

	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: 2, Reason: "อื่นๆ", Lot: &models.LotTarget{LotID: a.ID.Hex()}}); err != nil {
		t.Fatal(err)
	}
	if s.remaining(a.ID) != 5 {
		t.Fatalf("named lot A: %d, want 5", s.remaining(a.ID))
	}

	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: 4, Reason: "อื่นๆ",
		Lot: &models.LotTarget{LotNumber: "NEW", ExpiryDate: time.Now().AddDate(2, 0, 0).Format(dateLayout)}}); err != nil {
		t.Fatal(err)
	}
	var created models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"lot_number": "NEW"}).Decode(&created); err != nil || created.Remaining != 4 || created.Quantity != 4 {
		t.Fatalf("new lot %+v err=%v", created, err)
	}

	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: 1, Reason: "อื่นๆ"}); err != nil {
		t.Fatal(err)
	}
	var rec models.StockAdjustment
	if err := s.mdb.StockAdjustments().FindOne(s.ctx, bson.M{"delta": 1}).Decode(&rec); err != nil || !rec.LotAssumed || rec.Lots[0].LotNumber != "NEW" {
		t.Fatalf("an unnamed increase goes to the latest-expiring lot, flagged: %+v err=%v", rec, err)
	}
	if s.remaining(b.ID) != 3 || s.stock(id) != 13 {
		t.Fatalf("B %d stock %d", s.remaining(b.ID), s.stock(id))
	}
	s.consistent()
}

func TestIncreaseSettlesOversoldFromTheLot(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	lot := s.lot(id, "A", 1, 6)
	s.oversell(id, 4) // 1 from A, 3 oversold
	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: 5, Reason: "นับสต็อก", Lot: &models.LotTarget{LotID: lot.ID.Hex()}}); err != nil {
		t.Fatal(err)
	}
	if s.remaining(lot.ID) != 2 || s.stock(id) != 2 {
		t.Fatalf("lot %d stock %d, want 2 and 2", s.remaining(lot.ID), s.stock(id))
	}
	s.consistent()
}

func TestDrugWithoutLotsKeepsStockAlone(t *testing.T) {
	s := newStore(t)
	id := s.drug(10)
	if _, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: 5, Reason: "อื่นๆ"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.mdb.DrugLots().CountDocuments(s.ctx, bson.M{"drug_id": id}); n != 0 || s.stock(id) != 15 {
		t.Fatalf("lots %d stock %d", n, s.stock(id))
	}
	_, err := Adjust(s.ctx, s.mdb, id.Hex(), models.StockAdjustmentInput{Delta: -16, Reason: "สูญหาย"})
	wantKind(t, err, refusal.Invalid)
}

func TestCountAdjustsEachDrugToTheCountedQuantity(t *testing.T) {
	s := newStore(t)
	up := s.drug(0)
	s.lot(up, "U", 4, 6)
	down := s.drug(0)
	d1 := s.lot(down, "D1", 5, 2)
	count, err := Count(s.ctx, s.mdb, models.StockCountInput{Items: []models.StockCountInputItem{
		{DrugID: up.Hex(), Counted: 6}, {DrugID: down.Hex(), Counted: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if count.Items[0].Delta != 2 || count.Items[1].Delta != -3 || s.stock(up) != 6 || s.remaining(d1.ID) != 2 {
		t.Fatalf("count %+v", count.Items)
	}
	s.consistent()
}

func TestWriteOffIsAllOrNothing(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	a := s.lot(id, "A", 3, 1)
	_, err := WriteOff(s.ctx, s.mdb, []string{a.ID.Hex(), bson.NewObjectID().Hex()})
	var lotErr *WriteOffError
	if !errors.As(err, &lotErr) {
		t.Fatalf("expected a WriteOffError, got %v", err)
	}
	if s.remaining(a.ID) != 3 || s.stock(id) != 3 {
		t.Fatal("a failed batch must write nothing off")
	}

	if n, err := WriteOff(s.ctx, s.mdb, []string{a.ID.Hex()}); err != nil || n != 1 {
		t.Fatalf("write-off: n=%d err=%v", n, err)
	}
	var lot models.DrugLot
	_ = s.mdb.DrugLots().FindOne(s.ctx, bson.M{"_id": a.ID}).Decode(&lot)
	if lot.Remaining != 0 || lot.WrittenOffAt == nil || s.stock(id) != 0 {
		t.Fatalf("written-off lot kept at zero: %+v stock %d", lot, s.stock(id))
	}
	if n, _ := s.mdb.LotWriteoffs().CountDocuments(s.ctx, bson.M{"lot_id": a.ID, "reason": "writeoff"}); n != 1 {
		t.Fatalf("expected a write-off record, got %d", n)
	}
	s.consistent()
}

func TestSalesNeverTakeFromAWrittenOffLot(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	a := s.lot(id, "A", 3, 1)
	if _, err := WriteOff(s.ctx, s.mdb, []string{a.ID.Hex()}); err != nil {
		t.Fatal(err)
	}
	// Goods come back into the written-off lot (a return).
	if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"_id": a.ID}, bson.M{"$inc": bson.M{"remaining": 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": id}, bson.M{"$inc": bson.M{"stock": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckAvailable(s.ctx, s.mdb, id, 1); err == nil {
		t.Fatal("stock only in a written-off lot is not sellable")
	}
}

func TestDeleteLotOnlyForALotNeverSoldFrom(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	sold := s.lot(id, "SOLD", 3, 1)
	s.oversell(id, 1) // takes from SOLD
	wantKind(t, DeleteLot(s.ctx, s.mdb, id.Hex(), sold.ID.Hex()), refusal.Conflict)

	mistake := s.lot(id, "TYPO", 4, 6)
	if err := DeleteLot(s.ctx, s.mdb, id.Hex(), mistake.ID.Hex()); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.mdb.LotWriteoffs().CountDocuments(s.ctx, bson.M{"lot_id": mistake.ID, "reason": "deleted"}); n != 1 {
		t.Fatalf("expected a deletion record, got %d", n)
	}
	if s.stock(id) != 2 {
		t.Fatalf("stock %d, want 2", s.stock(id))
	}
	s.consistent()
}

func TestDriftListsDrugsWhoseStockAndLotsDisagree(t *testing.T) {
	s := newStore(t)
	id := s.drug(0)
	s.lot(id, "A", 5, 6)
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": id}, bson.M{"$inc": bson.M{"stock": 3}}); err != nil {
		t.Fatal(err)
	}
	rows, err := Drift(s.ctx, s.mdb)
	if err != nil || len(rows) != 1 || rows[0].Difference != 3 || rows[0].LotRemaining != 5 {
		t.Fatalf("drift %+v err=%v", rows, err)
	}
}
