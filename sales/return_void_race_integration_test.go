package sales

import (
	"fmt"
	"sync"
	"testing"

	"pharmacy-pos/backend/models"
)

// A void and a Return of the same Sale can race. Whatever order they land in,
// the goods go back to stock once: the shop started with 10, sold 4, and must
// end with exactly 10. A Void that commits first makes the Return an Invalid
// refusal; a Return that commits first leaves the Void nothing to restore, so
// both may succeed. A refused Void is a Conflict.
func TestVoidAndReturnOfTheSameSaleNeverRestoreStockTwice(t *testing.T) {
	const rounds = 50
	for round := 0; round < rounds; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
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
			if returnErr != nil {
				wantKind(t, returnErr, Invalid)
			}
			if voidErr != nil {
				wantKind(t, voidErr, Conflict)
			}
		})
	}
}

// Two Returns of the same whole line can race too. Only one may put the goods
// back; the other finds nothing left to return and is refused.
func TestTwoReturnsOfTheSameSaleNeverRestoreStockTwice(t *testing.T) {
	const rounds = 50
	for round := 0; round < rounds; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			s := newShop(t)
			sale := s.sell(fmt.Sprintf("race-sale-%d", round), 4)
			returnInput := func(requestID string) models.DrugReturnInput {
				return models.DrugReturnInput{
					ClientRequestID: requestID,
					Reason:          "race",
					Items:           []models.ReturnItemInput{{SaleItemID: s.saleItemID(sale.ID), Qty: 4}},
				}
			}
			inputA := returnInput(fmt.Sprintf("race-ret-a-%d", round))
			inputB := returnInput(fmt.Sprintf("race-ret-b-%d", round))

			start := make(chan struct{})
			var wg sync.WaitGroup
			var errA, errB error
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, _, errA = Return(s.ctx, s.mdb, sale.ID, inputA)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, _, errB = Return(s.ctx, s.mdb, sale.ID, inputB)
			}()
			close(start)
			wg.Wait()

			if drug, lot := s.stock(); drug != 10 || lot != 10 {
				t.Fatalf("round %d: stock drug=%d lot=%d after return(%v) + return(%v); want 10/10", round, drug, lot, errA, errB)
			}
			if errA == nil && errB == nil {
				t.Fatalf("round %d: both returns succeeded", round)
			}
			loser := errA
			if loser == nil {
				loser = errB
			}
			wantKind(t, loser, Invalid)
		})
	}
}
