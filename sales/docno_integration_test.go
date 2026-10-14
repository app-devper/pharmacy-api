package sales

import (
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestTheFirstBillsOfADaySoldAtOnceAllGetTheirOwnNumber(t *testing.T) {
	for round := 0; round < 10; round++ {
		s := newShop(t)
		if _, err := s.mdb.DrugLots().UpdateOne(s.ctx, bson.M{"drug_id": s.drugID}, bson.M{"$set": bson.M{"remaining": 100, "quantity": 100}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": s.drugID}, bson.M{"$set": bson.M{"stock": 100}}); err != nil {
			t.Fatal(err)
		}
		const at = 8
		var wg sync.WaitGroup
		errs := make(chan error, at)
		start := make(chan struct{})
		for i := 0; i < at; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _, err := Sell(s.ctx, s.mdb, s.saleInput("", 1))
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}
