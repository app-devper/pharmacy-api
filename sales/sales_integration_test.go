package sales

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
)

// These tests need a MongoDB replica set (transactions and unique indexes are
// the point). CI starts one; locally set MONGO_TEST_URI, e.g.
// mongodb://localhost:27017/?replicaSet=rs0&directConnection=true.

var testMgr *db.Manager

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("sales: MONGO_TEST_URI not set; skipping integration tests")
		os.Exit(0)
	}
	prefix := fmt.Sprintf("sales_test_%d", time.Now().UnixNano())
	testMgr = db.NewManager(uri, prefix)
	code := m.Run()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err == nil {
		ctx := context.Background()
		names, _ := client.ListDatabaseNames(ctx, bson.M{"name": bson.M{"$regex": "^" + prefix}})
		for _, n := range names {
			_ = client.Database(n).Drop(ctx)
		}
		_ = client.Disconnect(ctx)
	}
	os.Exit(code)
}

var tenantSeq int
var tenantMu sync.Mutex

// shop is a fresh tenant with one drug: 10 in stock, all in one lot.
type shop struct {
	t      *testing.T
	ctx    context.Context
	mdb    *db.MongoDB
	drugID bson.ObjectID
}

func newShop(t *testing.T) *shop {
	t.Helper()
	tenantMu.Lock()
	tenantSeq++
	client := fmt.Sprintf("t%d", tenantSeq)
	tenantMu.Unlock()
	mdb, err := testMgr.ForClient(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := mdb.Drugs().InsertOne(ctx, models.Drug{Name: "Paracetamol", SellPrice: 10, CostPrice: 4, Stock: 10})
	if err != nil {
		t.Fatal(err)
	}
	drugID := res.InsertedID.(bson.ObjectID)
	if _, err := mdb.DrugLots().InsertOne(ctx, models.DrugLot{
		DrugID: drugID, LotNumber: "L1", ExpiryDate: time.Now().AddDate(1, 0, 0), Quantity: 10, Remaining: 10,
	}); err != nil {
		t.Fatal(err)
	}
	return &shop{t: t, ctx: ctx, mdb: mdb, drugID: drugID}
}

func (s *shop) saleInput(requestID string, qty int) models.SaleInput {
	return models.SaleInput{
		ClientRequestID: requestID,
		Items:           []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: qty, Price: 10}},
		Received:        1000,
	}
}

func (s *shop) stock() (drug, lot int) {
	s.t.Helper()
	var d models.Drug
	if err := s.mdb.Drugs().FindOne(s.ctx, bson.M{"_id": s.drugID}).Decode(&d); err != nil {
		s.t.Fatal(err)
	}
	var l models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"drug_id": s.drugID}).Decode(&l); err != nil {
		s.t.Fatal(err)
	}
	return d.Stock, l.Remaining
}

func (s *shop) count(c *mongo.Collection) int64 {
	s.t.Helper()
	n, err := c.CountDocuments(s.ctx, bson.M{})
	if err != nil {
		s.t.Fatal(err)
	}
	return n
}

func (s *shop) sell(requestID string, qty int) models.SaleResponse {
	s.t.Helper()
	out, _, err := Sell(s.ctx, s.mdb, s.saleInput(requestID, qty))
	if err != nil {
		s.t.Fatalf("sell: %v", err)
	}
	return out
}

func (s *shop) saleItemID(saleID string) string {
	s.t.Helper()
	oid, _ := bson.ObjectIDFromHex(saleID)
	var item models.SaleItem
	if err := s.mdb.SaleItems().FindOne(s.ctx, bson.M{"sale_id": oid}).Decode(&item); err != nil {
		s.t.Fatal(err)
	}
	return item.ID.Hex()
}

func wantKind(t *testing.T, err error, kind Kind) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("expected refusal kind %d, got %v", kind, err)
	}
}

func TestSellRepeatedRequestReturnsTheOriginalSaleOnce(t *testing.T) {
	s := newShop(t)
	first := s.sell("req-1", 3)
	again, replayed, err := Sell(s.ctx, s.mdb, s.saleInput("req-1", 3))
	if err != nil || !replayed {
		t.Fatalf("expected replay, got replayed=%v err=%v", replayed, err)
	}
	if again.ID != first.ID || again.BillNo != first.BillNo || again.Total != first.Total {
		t.Fatalf("replay %+v differs from original %+v", again, first)
	}
	if n := s.count(s.mdb.Sales()); n != 1 {
		t.Fatalf("expected 1 sale, got %d", n)
	}
	if drug, lot := s.stock(); drug != 7 || lot != 7 {
		t.Fatalf("stock taken twice: drug=%d lot=%d, want 7", drug, lot)
	}
}

func TestSellConcurrentRepeatsRecordOneSale(t *testing.T) {
	s := newShop(t)
	var wg sync.WaitGroup
	ids := make([]string, 4)
	errs := make([]error, 4)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, _, err := Sell(s.ctx, s.mdb, s.saleInput("req-race", 2))
			ids[i], errs[i] = out.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("attempt %d: id=%s err=%v (first id %s)", i, ids[i], errs[i], ids[0])
		}
	}
	if n := s.count(s.mdb.Sales()); n != 1 {
		t.Fatalf("expected 1 sale, got %d", n)
	}
	if drug, _ := s.stock(); drug != 8 {
		t.Fatalf("drug stock %d, want 8", drug)
	}
}

func TestSellRefusesReusedRequestIDWithDifferentContent(t *testing.T) {
	s := newShop(t)
	s.sell("req-1", 3)
	_, _, err := Sell(s.ctx, s.mdb, s.saleInput("req-1", 4))
	if !errors.Is(err, ErrRequestReused) {
		t.Fatalf("expected ErrRequestReused, got %v", err)
	}
	if drug, _ := s.stock(); drug != 7 {
		t.Fatalf("drug stock %d, want 7", drug)
	}
}

func TestSellWithoutRequestIDSellsEachTime(t *testing.T) {
	s := newShop(t)
	s.sell("", 1)
	s.sell("", 1)
	if n := s.count(s.mdb.Sales()); n != 2 {
		t.Fatalf("expected 2 sales, got %d", n)
	}
}

func TestSellRefusesMoreThanStock(t *testing.T) {
	s := newShop(t)
	_, _, err := Sell(s.ctx, s.mdb, s.saleInput("req-1", 11))
	wantKind(t, err, Invalid)
}

func TestRepeatedPartialReturnRestoresStockOnce(t *testing.T) {
	s := newShop(t)
	sale := s.sell("sale-1", 5)
	input := models.DrugReturnInput{
		ClientRequestID: "ret-1",
		Reason:          "wrong drug",
		Items:           []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 1}},
	}
	first, replayed, err := Return(s.ctx, s.mdb, sale.ID, input)
	if err != nil || replayed {
		t.Fatalf("first return: replayed=%v err=%v", replayed, err)
	}
	again, replayed, err := Return(s.ctx, s.mdb, sale.ID, input)
	if err != nil || !replayed || again.ID != first.ID || again.ReturnNo != first.ReturnNo {
		t.Fatalf("expected replay of %s, got %+v replayed=%v err=%v", first.ReturnNo, again, replayed, err)
	}
	if n := s.count(s.mdb.DrugReturns()); n != 1 {
		t.Fatalf("expected 1 return, got %d", n)
	}
	if drug, lot := s.stock(); drug != 6 || lot != 6 {
		t.Fatalf("stock restored twice: drug=%d lot=%d, want 6", drug, lot)
	}
}

func TestReturnRefusesReusedRequestIDWithDifferentContent(t *testing.T) {
	s := newShop(t)
	sale := s.sell("sale-1", 5)
	item := s.saleItemID(sale.ID)
	ret := func(qty int) error {
		_, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
			ClientRequestID: "ret-1", Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: item, Qty: qty}},
		})
		return err
	}
	if err := ret(1); err != nil {
		t.Fatal(err)
	}
	if err := ret(2); !errors.Is(err, ErrRequestReused) {
		t.Fatalf("expected ErrRequestReused, got %v", err)
	}
}

func TestReturnCannotExceedWhatWasSold(t *testing.T) {
	s := newShop(t)
	sale := s.sell("sale-1", 2)
	_, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 3}},
	})
	wantKind(t, err, Invalid)
}

func TestVoidTwiceIsAConflictAndRestoresStockOnce(t *testing.T) {
	s := newShop(t)
	sale := s.sell("sale-1", 4)
	if err := Void(s.ctx, s.mdb, sale.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, Void(s.ctx, s.mdb, sale.ID, "mistake"), Conflict)
	if drug, lot := s.stock(); drug != 10 || lot != 10 {
		t.Fatalf("stock after void: drug=%d lot=%d, want 10", drug, lot)
	}
	wantKind(t, Void(s.ctx, s.mdb, bson.NewObjectID().Hex(), ""), NotFound)
}
