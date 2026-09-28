package receiving

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

var testMgr *db.Manager

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("receiving: MONGO_TEST_URI not set; skipping integration tests")
		os.Exit(0)
	}
	prefix := fmt.Sprintf("receiving_test_%d", time.Now().UnixNano())
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

var seq int

type shop struct {
	t      *testing.T
	ctx    context.Context
	mdb    *db.MongoDB
	drugID bson.ObjectID
}

func newShop(t *testing.T) *shop {
	t.Helper()
	seq++
	mdb, err := testMgr.ForClient(fmt.Sprintf("r%d", seq))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := mdb.Drugs().InsertOne(ctx, models.Drug{Name: "Tramadol 50", RegNo: "1A 1/60", Unit: "แคปซูล", SellPrice: 5, CostPrice: 2})
	if err != nil {
		t.Fatal(err)
	}
	return &shop{t: t, ctx: ctx, mdb: mdb, drugID: res.InsertedID.(bson.ObjectID)}
}

func (s *shop) line(lot, expiry string) models.POItemInput {
	return models.POItemInput{DrugID: s.drugID.Hex(), DrugName: "typed by the client", LotNumber: lot, ExpiryDate: expiry, Qty: 100, CostPrice: 1.5}
}

func (s *shop) draft(lines ...models.POItemInput) models.PurchaseOrder {
	s.t.Helper()
	po, err := Draft(s.ctx, s.mdb, models.POInput{Supplier: "Zuellig", InvoiceNo: "INV-9", ReceiveDate: "2026-09-20", Items: lines})
	if err != nil {
		s.t.Fatal(err)
	}
	return po
}

func (s *shop) count(c *mongo.Collection) int64 {
	s.t.Helper()
	n, err := c.CountDocuments(s.ctx, bson.M{})
	if err != nil {
		s.t.Fatal(err)
	}
	return n
}

func wantKind(t *testing.T, err error, kind refusal.Kind) {
	t.Helper()
	var e *refusal.Error
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("expected refusal kind %d, got %v", kind, err)
	}
}

const expiry = "2028-01-31"

// Confirming receives the lot, adds its stock, records ขย.9 from the catalog
// and the receive day, and closes the draft; a second confirm is refused.
func TestConfirmReceivesLotsAndRecordsKy9FromTheCatalog(t *testing.T) {
	s := newShop(t)
	po := s.draft(s.line("L1", expiry))
	if po.Items[0].DrugName != "Tramadol 50" {
		t.Fatalf("draft kept the client's name %q", po.Items[0].DrugName)
	}
	done, err := Confirm(s.ctx, s.mdb, po.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "confirmed" {
		t.Fatalf("status %q", done.Status)
	}
	var lot models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"drug_id": s.drugID}).Decode(&lot); err != nil {
		t.Fatal(err)
	}
	if lot.DrugName != "Tramadol 50" || lot.Remaining != 100 || lot.Origin != models.LotReceived {
		t.Fatalf("lot %+v", lot)
	}
	var drug models.Drug
	_ = s.mdb.Drugs().FindOne(s.ctx, bson.M{"_id": s.drugID}).Decode(&drug)
	if drug.Stock != 100 {
		t.Fatalf("stock %d", drug.Stock)
	}
	var ky9 models.Ky9
	if err := s.mdb.Ky9().FindOne(s.ctx, bson.M{}).Decode(&ky9); err != nil {
		t.Fatal(err)
	}
	if ky9.DrugName != "Tramadol 50" || ky9.RegNo != "1A 1/60" || ky9.Unit != "แคปซูล" || ky9.Date != "2026-09-20" ||
		ky9.TotalValue != 150 || ky9.Seller != "Zuellig" || ky9.InvoiceNo != "INV-9" {
		t.Fatalf("ky9 %+v", ky9)
	}

	_, err = Confirm(s.ctx, s.mdb, po.ID.Hex())
	wantKind(t, err, refusal.Conflict)
	if s.count(s.mdb.DrugLots()) != 1 || s.count(s.mdb.Ky9()) != 1 {
		t.Fatal("a second confirm wrote again")
	}
	_, err = Revise(s.ctx, s.mdb, po.ID.Hex(), models.POInput{Items: []models.POItemInput{s.line("L2", expiry)}})
	wantKind(t, err, refusal.Conflict)
	wantKind(t, Discard(s.ctx, s.mdb, po.ID.Hex()), refusal.Conflict)
}

// A draft may leave lot and expiry for later; confirming names every line
// that still lacks them and writes nothing.
func TestConfirmNamesEveryIncompleteLine(t *testing.T) {
	s := newShop(t)
	po := s.draft(s.line("L1", expiry), s.line("", expiry), s.line("L3", ""))
	_, err := Confirm(s.ctx, s.mdb, po.ID.Hex())
	wantKind(t, err, refusal.Invalid)
	if !strings.Contains(err.Error(), "รายการที่ 2") || !strings.Contains(err.Error(), "รายการที่ 3") {
		t.Fatalf("message %q should name lines 2 and 3", err)
	}
	if s.count(s.mdb.DrugLots())+s.count(s.mdb.Ky9()) != 0 {
		t.Fatal("an incomplete confirm wrote lots or ขย.9")
	}
}

func TestDraftRefusesWhatCanNeverBeConfirmed(t *testing.T) {
	s := newShop(t)
	bad := []models.POItemInput{
		s.line("L1", "31/01/2028"),
		{DrugID: s.drugID.Hex(), Qty: 0},
		{DrugID: bson.NewObjectID().Hex(), Qty: 1},
	}
	for _, line := range bad {
		_, err := Draft(s.ctx, s.mdb, models.POInput{Items: []models.POItemInput{line}})
		wantKind(t, err, refusal.Invalid)
	}
	if s.count(s.mdb.PurchaseOrders()) != 0 {
		t.Fatal("a refused draft was stored")
	}
}
