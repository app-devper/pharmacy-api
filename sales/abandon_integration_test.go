package sales

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func (s *shop) abandon(kind, requestID string) (models.Abandonment, bool, error) {
	return Abandon(asUser(s.ctx, "admin-1"), s.mdb, models.AbandonInput{
		ClientRequestID: requestID,
		Kind:            kind,
		Payload:         s.saleInput(requestID, 1),
		Reason:          "customer left",
	})
}

func TestAbandonedSaleCannotBeSoldLater(t *testing.T) {
	s := newShop(t)
	a, replayed, err := s.abandon(models.AbandonSale, "req-a")
	if err != nil || replayed {
		t.Fatalf("abandon: %v replayed=%v", err, replayed)
	}
	if a.By != "admin-1" || a.Reason != "customer left" || a.Payload == "" || a.Payload == "null" {
		t.Fatalf("audit record %+v", a)
	}
	_, _, err = Sell(s.ctx, s.mdb, s.saleInput("req-a", 1))
	wantKind(t, err, Conflict)
	if drug, lot := s.stock(); drug != 10 || lot != 10 {
		t.Fatalf("stock %d/%d, want untouched 10/10", drug, lot)
	}
	again, replayed, err := s.abandon(models.AbandonSale, "req-a")
	if err != nil || !replayed || again.ID != a.ID {
		t.Fatalf("repeat abandon: %v replayed=%v id %v vs %v", err, replayed, again.ID, a.ID)
	}
}

func TestRecordedSaleCannotBeAbandoned(t *testing.T) {
	s := newShop(t)
	sold := s.sell("req-b", 2)
	_, _, err := s.abandon(models.AbandonSale, "req-b")
	var already *AlreadySold
	if !errors.As(err, &already) || already.Sale.ID != sold.ID || already.Sale.BillNo != sold.BillNo {
		t.Fatalf("want AlreadySold with the sale, got %v", err)
	}
	if n := s.count(s.mdb.Abandonments()); n != 0 {
		t.Fatalf("%d abandonments recorded", n)
	}
}

// Selling and abandoning the same queued sale at once: exactly one wins. The
// abandonment starts after a random delay so it lands inside the sale's
// transaction on some rounds.
func TestSaleAndAbandonmentExcludeEachOther(t *testing.T) {
	s := newShop(t)
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": s.drugID}, bson.M{"$set": bson.M{"stock": 100}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"drug_id": s.drugID}, bson.M{"$set": bson.M{"remaining": 100, "quantity": 100}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("race-%d", i)
		var wg sync.WaitGroup
		var sellErr, abandonErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, _, sellErr = Sell(s.ctx, s.mdb, s.saleInput(id, 1)) }()
		delay := time.Duration(rand.Intn(8000)) * time.Microsecond
		go func() {
			defer wg.Done()
			time.Sleep(delay)
			_, _, abandonErr = s.abandon(models.AbandonSale, id)
		}()
		wg.Wait()
		var already *AlreadySold
		soldWon := sellErr == nil && errors.As(abandonErr, &already)
		abandonWon := abandonErr == nil && sellErr != nil
		if soldWon == abandonWon {
			t.Fatalf("%s: sell %v, abandon %v; want exactly one to win", id, sellErr, abandonErr)
		}
		if abandonWon {
			wantKind(t, sellErr, Conflict)
		}
	}
}

func TestRefusedKyFormsOfARecordedSaleCanBeClosed(t *testing.T) {
	s := newShop(t)
	sold := s.sell("req-k", 1)
	a, _, err := s.abandon(models.AbandonKyForms, "req-k")
	if err != nil {
		t.Fatal(err)
	}
	if a.SaleID != sold.ID || a.BillNo != sold.BillNo {
		t.Fatalf("ky abandonment %+v not linked to sale %s", a, sold.ID)
	}
	if _, _, err := s.abandon(models.AbandonKyForms, "req-unknown"); err == nil {
		t.Fatal("closing KY forms of an unknown sale should be refused")
	} else {
		wantKind(t, err, NotFound)
	}
	list, err := Abandoned(s.ctx, s.mdb, 10)
	if err != nil || len(list) != 1 || list[0].Kind != models.AbandonKyForms {
		t.Fatalf("list %+v %v", list, err)
	}
}

func TestAbandonNeedsARequestIDKindAndReason(t *testing.T) {
	s := newShop(t)
	for _, in := range []models.AbandonInput{
		{Kind: models.AbandonSale, Reason: "r"},
		{ClientRequestID: "x", Kind: "other", Reason: "r"},
		{ClientRequestID: "x", Kind: models.AbandonSale, Reason: "  "},
	} {
		_, _, err := Abandon(s.ctx, s.mdb, in)
		wantKind(t, err, Invalid)
	}
}
