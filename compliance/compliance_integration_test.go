package compliance_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Integration tests against a MongoDB replica set (MONGO_TEST_URI).

var testMgr *db.Manager

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("compliance: MONGO_TEST_URI not set; skipping integration tests")
		os.Exit(0)
	}
	prefix := fmt.Sprintf("compliance_test_%d", time.Now().UnixNano())
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

func shop(t *testing.T) (context.Context, *db.MongoDB) {
	t.Helper()
	seq++
	mdb, err := testMgr.ForClient(fmt.Sprintf("c%d", seq))
	if err != nil {
		t.Fatal(err)
	}
	return context.Background(), mdb
}

func TestAMonthsRegisterHoldsOnlyThatMonthsRows(t *testing.T) {
	ctx, mdb := shop(t)
	now := time.Now()
	for _, d := range []string{"2026-05-31", "2026-05-01", "2026-06-01", "2026-04-30"} {
		if _, err := compliance.RecordKy9(ctx, mdb, models.Ky9Input{Date: d, DrugName: "x", Qty: 2, PricePerUnit: 3}, now); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := compliance.Month[models.Ky9](ctx, mdb, "2026-05", compliance.OldestFirst)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Date != "2026-05-01" || rows[1].Date != "2026-05-31" || rows[0].TotalValue != 6 {
		t.Fatalf("may register %+v", rows)
	}
	all, _ := compliance.Month[models.Ky9](ctx, mdb, "", compliance.NewestFirst)
	if len(all) != 4 || all[0].Date != "2026-06-01" {
		t.Fatalf("whole register %+v", all)
	}
}

func TestAManualRowTakesTheShopDefaults(t *testing.T) {
	ctx, mdb := shop(t)
	if _, err := mdb.Settings().InsertOne(ctx, bson.M{"key": db.SettingsKey,
		"ky": bson.M{"default_buyer_address": "ร้านยา"}, "pharmacist": bson.M{"name": "ภญ.ก"}}); err != nil {
		t.Fatal(err)
	}
	d := compliance.LoadDefaults(ctx, mdb)
	row10, err := compliance.RecordKy10(ctx, mdb, models.Ky10Input{Date: "2026-05-17", DrugName: "x", Qty: 1, BuyerName: "B"}, d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row11, err := compliance.RecordKy11(ctx, mdb, models.Ky11Input{Date: "2026-05-17", DrugName: "x", Qty: 1, BuyerName: "B", Purpose: "ไอ"}, d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if row10.BuyerAddress != "ร้านยา" || row11.Pharmacist != "ภญ.ก" || row10.ID.IsZero() {
		t.Fatalf("rows %+v %+v", row10, row11)
	}
}

func TestASalesRowsAreReadBackBySale(t *testing.T) {
	ctx, mdb := shop(t)
	c, err := compliance.PrepareCapture(models.SaleKyCapture{
		Ky10: &models.Ky10Capture{BuyerName: "B", BuyerAddress: "A"},
		Ky12: &models.Ky12Capture{RxNo: "RX", PatientName: "P", Doctor: "D"},
	}, []compliance.Form{compliance.Ky10, compliance.Ky12}, compliance.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	lines := []compliance.SaleLine{{DrugName: "x", Unit: "แผง", Qty: 1, Paid: 36, Balance: 4}}
	for _, f := range []compliance.Form{compliance.Ky10, compliance.Ky12} {
		if err := compliance.RecordSale(ctx, mdb, f, "s1", "2026-05-17", c, lines, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := compliance.BySale(ctx, mdb, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ky10) != 1 || len(got.Ky11) != 0 || len(got.Ky12) != 1 || got.Ky10[0].Balance != 4 || got.Ky12[0].TotalValue != 36 || got.Ky12[0].Status != compliance.PaidStatus {
		t.Fatalf("by sale %+v", got)
	}
}
