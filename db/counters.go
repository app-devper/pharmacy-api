package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NextDocNo is the next document number of kind prefix on a business day:
// PREFIX-YYMMDD-NNN, counting from 001 each day. day must already be in the
// shop's timezone. In a transaction context the number is taken in the
// transaction.
func (m *MongoDB) NextDocNo(ctx context.Context, prefix string, day time.Time) (string, error) {
	key := day.Format("060102")
	var counter struct {
		Seq int `bson:"seq"`
	}
	if err := m.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": prefix + "-" + key},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter); err != nil {
		return "", fmt.Errorf("%s number: %w", prefix, err)
	}
	return fmt.Sprintf("%s-%s-%03d", prefix, key, counter.Seq), nil
}
