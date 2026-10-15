package catalog

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

var (
	testMgr   *db.Manager
	tenantMu  sync.Mutex
	tenantSeq int
)

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("catalog: MONGO_TEST_URI not set; running unit tests only")
		os.Exit(m.Run())
	}
	prefix := fmt.Sprintf("catalog_test_%d", time.Now().UnixNano())
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

func shop(t *testing.T) (context.Context, *db.MongoDB) {
	t.Helper()
	if testMgr == nil {
		t.Skip("MONGO_TEST_URI not set")
	}
	tenantMu.Lock()
	tenantSeq++
	client := fmt.Sprintf("c%d", tenantSeq)
	tenantMu.Unlock()
	mdb, err := testMgr.ForClient(client)
	if err != nil {
		t.Fatal(err)
	}
	return context.Background(), mdb
}

func wantKind(t *testing.T, err error, kind refusal.Kind) {
	t.Helper()
	var e *refusal.Error
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("expected refusal kind %d, got %v", kind, err)
	}
}

func lotsOf(t *testing.T, ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID) []models.DrugLot {
	t.Helper()
	cur, err := mdb.DrugLots().Find(ctx, bson.M{"drug_id": drugID})
	if err != nil {
		t.Fatal(err)
	}
	var lots []models.DrugLot
	if err := cur.All(ctx, &lots); err != nil {
		t.Fatal(err)
	}
	return lots
}

func TestRegisterOpensStockInALotThatInheritsTheSellPrice(t *testing.T) {
	ctx, mdb := shop(t)
	expiry := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	drug, err := Register(ctx, mdb, models.DrugInput{
		Name: "Paracetamol", SellPrice: 10, CostPrice: 4, Stock: 20,
		CreateLot: &models.DrugLotInput{LotNumber: "L1", ExpiryDate: expiry, Quantity: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	lots := lotsOf(t, ctx, mdb, drug.ID)
	if len(lots) != 1 || lots[0].Remaining != 20 || lots[0].Origin != models.LotOpening {
		t.Fatalf("want one opening lot of 20, got %+v", lots)
	}
	if lots[0].SellPrice != nil {
		t.Fatalf("opening lot must inherit the drug's sell price, got %v", *lots[0].SellPrice)
	}
	if lots[0].CostPrice == nil || *lots[0].CostPrice != 4 {
		t.Fatalf("opening lot keeps the cost it came in at, got %v", lots[0].CostPrice)
	}
}

func TestRegisterRefusesADuplicateBarcode(t *testing.T) {
	ctx, mdb := shop(t)
	if _, err := Register(ctx, mdb, models.DrugInput{Name: "A", Barcode: "885"}); err != nil {
		t.Fatal(err)
	}
	_, err := Register(ctx, mdb, models.DrugInput{Name: "B", Barcode: "885"})
	wantKind(t, err, refusal.Conflict)
}

func TestImportReportsEachRowAndOpensStockTheSameWayAsRegister(t *testing.T) {
	ctx, mdb := shop(t)
	got, err := Import(ctx, mdb, []models.DrugInput{
		{Name: "Amoxicillin", Barcode: "111", SellPrice: 10, CostPrice: 5, Stock: 7},
		{Name: " "},
		{Name: "Amoxicillin again", Barcode: "111"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Imported != 1 || len(got.Errors) != 2 {
		t.Fatalf("want 1 imported and 2 row errors, got %+v", got)
	}
	if got.Errors[0].Row != 3 || got.Errors[1].Row != 4 || got.Errors[1].Message != ErrDuplicateBarcode.Msg {
		t.Fatalf("unexpected row errors %+v", got.Errors)
	}
	var drug models.Drug
	if err := mdb.Drugs().FindOne(ctx, bson.M{"barcode": "111"}).Decode(&drug); err != nil {
		t.Fatal(err)
	}
	lots := lotsOf(t, ctx, mdb, drug.ID)
	if len(lots) != 1 || lots[0].Remaining != 7 || lots[0].SellPrice != nil {
		t.Fatalf("want one opening lot of 7 inheriting the sell price, got %+v", lots)
	}
}

func TestImportRefusesAnEmptyOrOversizedBatch(t *testing.T) {
	ctx, mdb := shop(t)
	_, err := Import(ctx, mdb, nil)
	wantKind(t, err, refusal.Invalid)
	_, err = Import(ctx, mdb, make([]models.DrugInput, MaxImportRows+1))
	wantKind(t, err, refusal.Invalid)
}

func TestUpdateRefusesAMissingDrugAndADuplicateBarcode(t *testing.T) {
	ctx, mdb := shop(t)
	wantKind(t, Update(ctx, mdb, "nope", models.DrugUpdate{Name: "X"}), refusal.Invalid)
	wantKind(t, Update(ctx, mdb, bson.NewObjectID().Hex(), models.DrugUpdate{Name: "X"}), refusal.NotFound)
	if _, err := Register(ctx, mdb, models.DrugInput{Name: "A", Barcode: "222"}); err != nil {
		t.Fatal(err)
	}
	b, err := Register(ctx, mdb, models.DrugInput{Name: "B", Barcode: "333"})
	if err != nil {
		t.Fatal(err)
	}
	wantKind(t, Update(ctx, mdb, b.ID.Hex(), models.DrugUpdate{Name: "B", Barcode: "222"}), refusal.Conflict)
}
