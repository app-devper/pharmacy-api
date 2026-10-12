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
	bills  int
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

// drug adds a drug in stock.
func (s *shop) drug(name string, stock int) bson.ObjectID {
	s.t.Helper()
	res, err := s.mdb.Drugs().InsertOne(s.ctx, models.Drug{Name: name, SellPrice: 10, CostPrice: 4, Stock: stock})
	if err != nil {
		s.t.Fatal(err)
	}
	return res.InsertedID.(bson.ObjectID)
}

// soldAt records a confirmed bill of one line at a given moment, the way a
// late or replayed sale lands, bypassing the sales module's clock.
func (s *shop) soldAt(at time.Time, drugID bson.ObjectID, qty int, subtotal, cost float64) {
	s.t.Helper()
	s.bills++
	bill := s.bills
	res, err := s.mdb.Sales().InsertOne(s.ctx, models.Sale{BillNo: fmt.Sprintf("T-%d", bill), Total: subtotal, Received: subtotal, SoldAt: at})
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.mdb.SaleItems().InsertOne(s.ctx, models.SaleItem{
		SaleID: res.InsertedID.(bson.ObjectID), DrugID: drugID, DrugName: "x", Qty: qty, Subtotal: subtotal, CostSubtotal: cost,
	}); err != nil {
		s.t.Fatal(err)
	}
}

// today is today's End-of-day report, today's daily-chart total, and the
// shop drug's row of today's profit report.
func (s *shop) today() (models.DrugProfit, models.EodReport, float64) {
	s.t.Helper()
	now := time.Now()
	day, err := reporting.Day(s.ctx, s.mdb, now, s.tz)
	if err != nil {
		s.t.Fatal(err)
	}
	daily, err := reporting.Daily(s.ctx, s.mdb, now, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	total := 0.0
	if len(daily) == 1 {
		total = daily[0].Total
	}
	date := now.In(s.tz).Format("2006-01-02")
	profit, err := reporting.Profit(s.ctx, s.mdb, now, date, date)
	if err != nil {
		s.t.Fatal(err)
	}
	var drug models.DrugProfit
	for _, row := range profit.ByDrug {
		if row.DrugID == s.drugID.Hex() {
			drug = row
		}
	}
	return drug, day, total
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
	if !near(drug.Cost, 16) || drug.QtySold != 4 {
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
	s.soldAt(day.Add(30*time.Minute), s.drugID, 5, 50, 20)
	daily, err := reporting.Daily(s.ctx, s.mdb, time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 1 || daily[0].Day != day.Format("2006-01-02") || !near(daily[0].Total, 50) {
		t.Fatalf("daily %+v, want one row for %s", daily, day.Format("2006-01-02"))
	}
}

// SlowDrugs counts whole pharmacy days: a sale at 00:30 on the first day of
// the window means the drug sold, whatever the time of day the report runs.
func TestSlowDrugsCountsWholePharmacyDays(t *testing.T) {
	s := newShop(t)
	now := reporting.DayStart(time.Now(), s.tz).Add(23 * time.Hour)
	sold := s.drug("Sold early on day one", 5)
	idle := s.drug("Never sold", 5)
	s.soldAt(reporting.DayStart(now, s.tz).AddDate(0, 0, -7).Add(30*time.Minute), sold, 1, 10, 4)

	slow, err := reporting.SlowDrugs(s.ctx, s.mdb, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range slow {
		names = append(names, d.DrugName)
		if d.DrugID == sold.Hex() {
			t.Fatalf("slow drugs %v include a drug sold inside the window", names)
		}
	}
	found := false
	for _, d := range slow {
		found = found || d.DrugID == idle.Hex()
	}
	if !found {
		t.Fatalf("slow drugs %v leave out a drug that never sold", names)
	}
}

func TestTopDrugsRankByNetQuantityAndSkipFullyReturned(t *testing.T) {
	s := newShop(t)
	now := time.Now()
	a, b := s.drug("A", 10), s.drug("B", 10)
	s.soldAt(now, a, 2, 20, 8)
	s.soldAt(now, b, 5, 50, 20)
	s.sell(1, 0)
	if _, err := s.mdb.DrugReturns().InsertOne(s.ctx, models.DrugReturn{
		ReturnedAt: now, Refund: 10,
		Items: []models.ReturnItem{{DrugID: s.drugID, Qty: 1, Subtotal: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	top, err := reporting.TopDrugs(s.ctx, s.mdb, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].DrugID != b.Hex() || top[1].DrugID != a.Hex() {
		t.Fatalf("top drugs %+v, want B then A and no fully returned drug", top)
	}
}

func TestProfitTotalsMarginsAndBillsOverTheDates(t *testing.T) {
	s := newShop(t)
	now := time.Now()
	today := reporting.DayStart(now, s.tz)
	s.soldAt(today.Add(time.Hour), s.drugID, 2, 20, 8)
	s.soldAt(today.AddDate(0, 0, -40), s.drugID, 9, 90, 36) // outside
	date := today.Format("2006-01-02")
	p, err := reporting.Profit(s.ctx, s.mdb, now, date, date)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary.Bills != 1 || !near(p.Summary.Revenue, 20) || !near(p.Summary.Profit, 12) || !near(p.Summary.Margin, 60) {
		t.Fatalf("summary %+v", p.Summary)
	}
	if len(p.ByDrug) != 1 || !near(p.ByDrug[0].Margin, 60) {
		t.Fatalf("by drug %+v", p.ByDrug)
	}
}

func TestReorderProjectsDemandOverTheLookahead(t *testing.T) {
	s := newShop(t)
	now := time.Now()
	short := s.drug("Short", 2)
	enough := s.drug("Enough", 100)
	out := s.drug("Out", 0)
	for _, id := range []bson.ObjectID{short, enough, out} {
		s.soldAt(now, id, 30, 300, 120) // 1 a day over 30 days
	}
	got, err := reporting.Reorder(s.ctx, s.mdb, now, 30, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].DrugID != out.Hex() || got[1].DrugID != short.Hex() {
		t.Fatalf("reorder %+v, want Out then Short", got)
	}
	if got[1].SuggestedQty != 12 || !near(got[1].DaysLeft, 2) {
		t.Fatalf("short %+v, want 14 needed less 2 on hand, 2 days left", got[1])
	}
}

func TestDashboardChartsAgreeWithTheirReports(t *testing.T) {
	s := newShop(t)
	now := time.Now()
	s.sell(2, 0)
	voided := s.sell(1, 0)
	if _, err := s.mdb.Sales().UpdateOne(s.ctx, bson.M{"bill_no": voided.BillNo}, bson.M{"$set": bson.M{"voided": true}}); err != nil {
		t.Fatal(err)
	}
	dash, err := reporting.Dashboard(s.ctx, s.mdb, now, 7, 5)
	if err != nil {
		t.Fatal(err)
	}
	daily, _ := reporting.Daily(s.ctx, s.mdb, now, 7)
	monthly, _ := reporting.Monthly(s.ctx, s.mdb, now, 12)
	if fmt.Sprint(dash.Daily) != fmt.Sprint(daily) || fmt.Sprint(dash.Monthly) != fmt.Sprint(monthly) {
		t.Fatalf("dashboard charts %v %v, reports %v %v", dash.Daily, dash.Monthly, daily, monthly)
	}
	if len(dash.RecentSales) != 1 || dash.RecentSales[0].Voided {
		t.Fatalf("recent sales %+v, want the one confirmed bill", dash.RecentSales)
	}
	if !near(dash.Summary.TodaySales, 20) || dash.Summary.TodayBills != 1 {
		t.Fatalf("summary %+v", dash.Summary)
	}
}
