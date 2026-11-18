package sales

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

func TestAggregateReturnsSumsPerLineAndRefund(t *testing.T) {
	// Arrange
	line := bson.NewObjectID()
	returns := []models.DrugReturn{
		{Items: []models.ReturnItem{{SaleItemID: line, Qty: 1}}, Refund: 10},
		{Items: []models.ReturnItem{{SaleItemID: line, Qty: 2}}, Refund: 15.5},
	}

	// Act
	byItem, refunded := aggregateReturns(returns)

	// Assert
	if byItem[line] != 3 {
		t.Fatalf("returned on line = %d, want 3", byItem[line])
	}
	if refunded != 25.5 {
		t.Fatalf("refunded = %v, want 25.5", refunded)
	}
}
