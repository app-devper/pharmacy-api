package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/inventory"
	"pharmacy-pos/backend/models"
)

// Writing off a lot used to delete it, and a later void or return of a bill
// taken from it failed with 500 (ADR-0007). The goods now go back to the
// written-off lot.
func TestVoidAndReturnAfterTheLotWasWrittenOff(t *testing.T) {
	s := newShop(t)
	var lot models.DrugLot
	if err := s.mdb.DrugLots().FindOne(s.ctx, bson.M{"drug_id": s.drugID}).Decode(&lot); err != nil {
		t.Fatal(err)
	}
	voided := s.sell("", 3)
	returned := s.sell("", 2)
	if _, err := inventory.WriteOff(s.ctx, s.mdb, []string{lot.ID.Hex()}); err != nil {
		t.Fatal(err)
	}
	if drug, _ := s.stock(); drug != 0 {
		t.Fatalf("stock after write-off %d, want 0", drug)
	}

	if err := Void(s.ctx, s.mdb, voided.ID, "mistake"); err != nil {
		t.Fatalf("void after write-off: %v", err)
	}
	if _, _, err := Return(s.ctx, s.mdb, returned.ID, models.DrugReturnInput{
		Reason: "r", Items: []models.ReturnItemInput{{SaleItemID: s.saleItemID(returned.ID), Qty: 1}},
	}); err != nil {
		t.Fatalf("return after write-off: %v", err)
	}
	drug, remaining := s.stock()
	if drug != 4 || remaining != 4 {
		t.Fatalf("goods back in the written-off lot: stock %d lot %d, want 4 and 4", drug, remaining)
	}
	if err := inventory.CheckAvailable(s.ctx, s.mdb, s.drugID, 1); err == nil {
		t.Fatal("goods back in a written-off lot must not be sold")
	}
}
