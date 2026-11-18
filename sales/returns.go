package sales

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Returns lists the Returns recorded against a Sale, newest first. A Sale
// with none (or an unknown one) yields an empty, non-nil slice.
func Returns(ctx context.Context, mdb *db.MongoDB, saleID string) ([]models.DrugReturn, error) {
	oid, err := bson.ObjectIDFromHex(saleID)
	if err != nil {
		return nil, invalid("invalid id")
	}
	cur, err := mdb.DrugReturns().Find(ctx, bson.M{"sale_id": oid},
		options.Find().SetSort(bson.D{{Key: "returned_at", Value: -1}, {Key: "_id", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	returns := []models.DrugReturn{}
	if err := cur.All(ctx, &returns); err != nil {
		return nil, err
	}
	return returns, nil
}
