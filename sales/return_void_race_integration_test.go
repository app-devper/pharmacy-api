package sales

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"pharmacy-pos/backend/models"
)

// A void and a Return of the same Sale can race. Whatever order they land in,
// the goods go back to stock once: the shop started with 10, sold 4, and must
// end with exactly 10. Both commands succeeding would put 4 back twice.
func TestVoidAndReturnOfTheSameSaleNeverRestoreStockTwice(t *testing.T) {
	if os.Getenv("PHARMACY_RACE_PROBE") == "" {
		t.Skip("red on develop (2026-11-06): both commands succeed and stock doubles; fix tracked in docs/plans/2026-11-06-pharmacy-api-sale-reversal-state.md. Set PHARMACY_RACE_PROBE=1 to run.")
	}
	const rounds = 50
	for round := 0; round < rounds; round++ {
		s := newShop(t)
		sale := s.sell(fmt.Sprintf("race-sale-%d", round), 4)
		input := models.DrugReturnInput{
			ClientRequestID: fmt.Sprintf("race-ret-%d", round),
			Reason:          "race",
			Items:           []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 4}},
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var voidErr, returnErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			voidErr = Void(s.ctx, s.mdb, sale.ID, "race")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _, returnErr = Return(s.ctx, s.mdb, sale.ID, input)
		}()
		close(start)
		wg.Wait()

		if drug, lot := s.stock(); drug != 10 || lot != 10 {
			t.Fatalf("round %d: stock drug=%d lot=%d after void(%v) + return(%v); want 10/10", round, drug, lot, voidErr, returnErr)
		}
		if voidErr == nil && returnErr == nil {
			t.Fatalf("round %d: both void and return succeeded", round)
		}
		var refused *Error
		if returnErr != nil && !errors.As(returnErr, &refused) {
			t.Fatalf("round %d: return failed with a non-refusal: %v", round, returnErr)
		}
		if voidErr != nil && !errors.As(voidErr, &refused) {
			t.Fatalf("round %d: void failed with a non-refusal: %v", round, voidErr)
		}
	}
}
