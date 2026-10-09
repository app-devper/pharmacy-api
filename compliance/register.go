package compliance

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Row is a row of one of the registers.
type Row interface {
	models.Ky9 | models.Ky10 | models.Ky11 | models.Ky12
}

func collection[T Row](mdb *db.MongoDB) *mongo.Collection {
	var zero T
	switch any(zero).(type) {
	case models.Ky9:
		return mdb.Ky9()
	case models.Ky10:
		return mdb.Ky10()
	case models.Ky11:
		return mdb.Ky11()
	default:
		return mdb.Ky12()
	}
}

// Order is the order a register is read in, by date.
type Order int

const (
	NewestFirst Order = -1 // the register screens
	OldestFirst Order = 1  // the printed register
)

// Month reads a register's rows dated in month (YYYY-MM; empty for every
// row). A malformed month is refused (refusal.Invalid).
func Month[T Row](ctx context.Context, mdb *db.MongoDB, month string, order Order) ([]T, error) {
	filter, err := monthFilter(month)
	if err != nil {
		return nil, err
	}
	cur, err := collection[T](mdb).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "date", Value: int(order)}, {Key: "_id", Value: int(order)}}))
	if err != nil {
		return nil, err
	}
	rows := []T{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// BySale reads the sale-register rows (ขย.10–12) recorded for a sale, in the
// order they were recorded.
func BySale(ctx context.Context, mdb *db.MongoDB, saleID string) (models.SaleKyLinkage, error) {
	var out models.SaleKyLinkage
	var err error
	if out.Ky10, err = forSale[models.Ky10](ctx, mdb, saleID); err != nil {
		return out, err
	}
	if out.Ky11, err = forSale[models.Ky11](ctx, mdb, saleID); err != nil {
		return out, err
	}
	out.Ky12, err = forSale[models.Ky12](ctx, mdb, saleID)
	return out, err
}

func forSale[T Row](ctx context.Context, mdb *db.MongoDB, saleID string) ([]T, error) {
	cur, err := collection[T](mdb).Find(ctx, bson.M{"sale_id": saleID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	rows := []T{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
