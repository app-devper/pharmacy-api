package sales

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/app-devper/um-api/sessionclient"
	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func asUser(ctx context.Context, userID string) context.Context {
	return sessionclient.WithPrincipal(ctx, sessionclient.Principal{UserID: userID, Role: sessionclient.RoleAdmin})
}

func (s *shop) close(name string) models.EodClose {
	s.t.Helper()
	closed, _, err := Close(asUser(s.ctx, "admin-1"), s.mdb, "", name)
	if err != nil {
		s.t.Fatalf("close: %v", err)
	}
	return closed
}

// closedDayMatchesLive checks the ADR-0006 invariant: a closed day's snapshot
// plus its adjustments equals what the live report computes now.
func (s *shop) closedDayMatchesLive() models.EodDay {
	s.t.Helper()
	day, err := Day(s.ctx, s.mdb, "")
	if err != nil {
		s.t.Fatal(err)
	}
	if day.Close == nil || day.Adjustments == nil {
		s.t.Fatalf("expected a closed day, got %+v", day)
	}
	tz := s.mdb.Timezone(s.ctx)
	today, _ := time.ParseInLocation(dayLayout, businessDay(time.Now(), tz), tz)
	live, err := liveReport(s.ctx, s.mdb, today, tz)
	if err != nil {
		s.t.Fatal(err)
	}
	a := day.Adjustments
	if a.AdjustedBillCount != live.BillCount || !near(a.AdjustedTotalSales, live.TotalSales) || !near(a.AdjustedNetCash, live.NetCash) {
		s.t.Fatalf("snapshot+adjustments (bills %d, sales %.2f, cash %.2f) != live (bills %d, sales %.2f, cash %.2f)",
			a.AdjustedBillCount, a.AdjustedTotalSales, a.AdjustedNetCash, live.BillCount, live.TotalSales, live.NetCash)
	}
	return day
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.001 }

func TestCloseSnapshotsTheDayAndRepeatReturnsIt(t *testing.T) {
	s := newShop(t)
	s.sell("", 3)
	s.sell("", 2)
	closed := s.close("Somchai")
	if closed.Report.BillCount != 2 || !near(closed.Report.TotalSales, 50) || closed.ClosedBy != "Somchai" || closed.ClosedByUserID != "admin-1" {
		t.Fatalf("unexpected close %+v", closed)
	}
	again, replayed, err := Close(asUser(s.ctx, "admin-2"), s.mdb, "", "Other")
	if err != nil || !replayed || again.ID != closed.ID || again.ClosedBy != "Somchai" {
		t.Fatalf("expected the first close replayed, got %+v replayed=%v err=%v", again, replayed, err)
	}
	day := s.closedDayMatchesLive()
	if len(day.Adjustments.Items) != 0 {
		t.Fatalf("expected no adjustments, got %+v", day.Adjustments.Items)
	}
}

func TestSaleAfterCloseIsALateSaleAdjustment(t *testing.T) {
	s := newShop(t)
	s.sell("", 1)
	s.close("")
	late := s.sell("", 2)
	day := s.closedDayMatchesLive()
	if day.BillCount != 1 || !near(day.TotalSales, 10) {
		t.Fatalf("snapshot changed: %+v", day.EodReport)
	}
	items := day.Adjustments.Items
	if len(items) != 1 || items[0].Kind != models.AdjustLateSale || items[0].RefNo != late.BillNo || !near(items[0].SalesDelta, 20) || items[0].BillDelta != 1 {
		t.Fatalf("unexpected adjustments %+v", items)
	}
}

func TestVoidAfterCloseAdjustsWithoutChangingTheSnapshot(t *testing.T) {
	s := newShop(t)
	sale := s.sell("", 3)
	s.close("")
	if err := Void(asUser(s.ctx, "admin-1"), s.mdb, sale.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	day := s.closedDayMatchesLive()
	if day.BillCount != 1 || !near(day.TotalSales, 30) {
		t.Fatalf("snapshot changed after void: %+v", day.EodReport)
	}
	items := day.Adjustments.Items
	if len(items) != 1 || items[0].Kind != models.AdjustVoid || items[0].By != "admin-1" || !near(items[0].SalesDelta, -30) {
		t.Fatalf("unexpected adjustments %+v", items)
	}
}

func TestReturnAfterCloseIsAnAdjustment(t *testing.T) {
	s := newShop(t)
	sale := s.sell("", 5)
	s.close("")
	if _, _, err := Return(s.ctx, s.mdb, sale.ID, models.DrugReturnInput{
		Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	items := s.closedDayMatchesLive().Adjustments.Items
	if len(items) != 1 || items[0].Kind != models.AdjustReturn || !near(items[0].SalesDelta, -10) {
		t.Fatalf("unexpected adjustments %+v", items)
	}
}

func TestOpenDayHasNoAdjustments(t *testing.T) {
	s := newShop(t)
	sale := s.sell("", 2)
	if err := Void(s.ctx, s.mdb, sale.ID, ""); err != nil {
		t.Fatal(err)
	}
	day, err := Day(s.ctx, s.mdb, "")
	if err != nil || day.Close != nil || day.Adjustments != nil || day.BillCount != 0 {
		t.Fatalf("expected a live open day with no bills, got %+v err=%v", day, err)
	}
	if n := s.count(s.mdb.EodAdjustments()); n != 0 {
		t.Fatalf("expected no adjustments for an open day, got %d", n)
	}
}

func TestCloseRefusesAFutureOrMalformedDay(t *testing.T) {
	s := newShop(t)
	tomorrow := time.Now().In(s.mdb.Timezone(s.ctx)).AddDate(0, 0, 1).Format(dayLayout)
	for _, date := range []string{tomorrow, "27/09/2026"} {
		_, _, err := Close(s.ctx, s.mdb, date, "")
		wantKind(t, err, Invalid)
	}
}

func TestPastDayCanBeClosed(t *testing.T) {
	s := newShop(t)
	yesterday := time.Now().In(s.mdb.Timezone(s.ctx)).AddDate(0, 0, -1).Format(dayLayout)
	closed, _, err := Close(s.ctx, s.mdb, yesterday, "")
	if err != nil || closed.Date != yesterday || closed.Report.BillCount != 0 {
		t.Fatalf("closing yesterday: %+v err=%v", closed, err)
	}
}

// A sale that commits while the day is being closed is either in the snapshot
// or a Late sale adjustment, never lost.
func TestSalesConcurrentWithCloseAreNeverLost(t *testing.T) {
	s := newShop(t)
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": s.drugID}, bson.M{"$set": bson.M{"stock": 100}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"drug_id": s.drugID}, bson.M{"$set": bson.M{"remaining": 100, "quantity": 100}}); err != nil {
		t.Fatal(err)
	}
	const sales = 12
	var wg sync.WaitGroup
	errs := make(chan error, sales+1)
	for i := 0; i < sales; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := Sell(s.ctx, s.mdb, s.saleInput("", 1))
			errs <- err
		}()
		if i == sales/2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, err := Close(asUser(s.ctx, "admin-1"), s.mdb, "", "")
				errs <- err
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	day := s.closedDayMatchesLive()
	if day.Adjustments.AdjustedBillCount != sales {
		t.Fatalf("expected %d bills after adjustments, got %d (snapshot %d + %d adjustments)",
			sales, day.Adjustments.AdjustedBillCount, day.BillCount, len(day.Adjustments.Items))
	}
}
