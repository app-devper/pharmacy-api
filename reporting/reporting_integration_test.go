package reporting_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/reporting"
	"pharmacy-pos/backend/sales"
)

// Integration tests against a MongoDB replica set (MONGO_TEST_URI).

var testMgr *db.Manager

func TestMain(m *testing.M) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		fmt.Println("reporting: MONGO_TEST_URI not set; skipping integration tests")
		os.Exit(0)
	}
	prefix := fmt.Sprintf("reporting_test_%d", time.Now().UnixNano())
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
	tz     *time.Location
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
	res, err := mdb.Drugs().InsertOne(ctx, models.Drug{Name: "Cetirizine", SellPrice: 10, CostPrice: 4, Stock: 100})
	if err != nil {
		t.Fatal(err)
	}
	drugID := res.InsertedID.(bson.ObjectID)
	if _, err := mdb.DrugLots().InsertOne(ctx, models.DrugLot{
		DrugID: drugID, LotNumber: "L1", ExpiryDate: time.Now().AddDate(1, 0, 0), Quantity: 100, Remaining: 100,
	}); err != nil {
		t.Fatal(err)
	}
	return &shop{t: t, ctx: ctx, mdb: mdb, tz: mdb.Timezone(ctx), drugID: drugID}
}

func (s *shop) sell(qty int, discount float64) models.SaleResponse {
	s.t.Helper()
	out, _, err := sales.Sell(s.ctx, s.mdb, models.SaleInput{
		Items:    []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: qty, Price: 10}},
		Discount: discount,
		Received: 1000,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return out
}

func (s *shop) today() (reporting.Line, models.EodReport, float64) {
	s.t.Helper()
	start := reporting.DayStart(time.Now(), s.tz)
	lines, err := reporting.Lines(s.ctx, s.mdb, start, start.AddDate(0, 0, 1))
	if err != nil {
		s.t.Fatal(err)
	}
	day, err := reporting.Day(s.ctx, s.mdb, time.Now(), s.tz)
	if err != nil {
		s.t.Fatal(err)
	}
	daily := reporting.Daily(lines, s.tz)
	total := 0.0
	if len(daily) == 1 {
		total = daily[0].Total
	}
	var byDrug reporting.Line
	for _, t := range reporting.ByDrug(lines) {
		byDrug = reporting.Line{Qty: t.Qty, Revenue: t.Revenue, Cost: t.Cost}
	}
	return byDrug, day, total
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

// The daily chart, the drug totals, and the End-of-day close agree on a day
// with a bill discount.
func TestReportsAgreeWithEndOfDayOnADiscountedBill(t *testing.T) {
	s := newShop(t)
	s.sell(3, 6) // 30 less a 6 bill discount: paid 24
	s.sell(1, 0) // paid 10
	drug, day, daily := s.today()
	if !near(day.TotalSales, 34) || !near(daily, 34) || !near(drug.Revenue, 34) {
		t.Fatalf("eod %.2f daily %.2f drug revenue %.2f, want 34", day.TotalSales, daily, drug.Revenue)
	}
	if !near(drug.Cost, 16) || drug.Qty != 4 {
		t.Fatalf("drug totals %+v", drug)
	}
}

// Returning a whole discounted bill refunds what the customer paid.
func TestReturningADiscountedBillRefundsWhatWasPaid(t *testing.T) {
	s := newShop(t)
	sale := s.sell(3, 6)
	oid, _ := bson.ObjectIDFromHex(sale.ID)
	var item models.SaleItem
	if err := s.mdb.SaleItems().FindOne(s.ctx, bson.M{"sale_id": oid}).Decode(&item); err != nil {
		t.Fatal(err)
	}
	ret, _, err := sales.Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: item.ID.Hex(), Qty: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !near(ret.Refund, 24) {
		t.Fatalf("refund %.2f, want 24 (what was paid)", ret.Refund)
	}
	drug, day, daily := s.today()
	if !near(day.TotalSales, 0) || !near(daily, 0) || !near(drug.Revenue, 0) {
		t.Fatalf("after a full return: eod %.2f daily %.2f revenue %.2f, want 0", day.TotalSales, daily, drug.Revenue)
	}
}

// A sale at 00:30 in the pharmacy's timezone belongs to that day, not the
// previous UTC day.
func TestDaysFollowThePharmacyTimezone(t *testing.T) {
	s := newShop(t)
	day := reporting.DayStart(time.Now(), s.tz).AddDate(0, 0, -1)
	at := day.Add(30 * time.Minute)
	res, err := s.mdb.Sales().InsertOne(s.ctx, models.Sale{BillNo: "T-1", Total: 50, Received: 50, SoldAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mdb.SaleItems().InsertOne(s.ctx, models.SaleItem{SaleID: res.InsertedID.(bson.ObjectID), DrugID: s.drugID, Qty: 5, Subtotal: 50}); err != nil {
		t.Fatal(err)
	}
	lines, err := reporting.Lines(s.ctx, s.mdb, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	daily := reporting.Daily(lines, s.tz)
	if len(daily) != 1 || daily[0].Day != day.Format("2006-01-02") || !near(daily[0].Total, 50) {
		t.Fatalf("daily %+v, want one row for %s", daily, day.Format("2006-01-02"))
	}
}
