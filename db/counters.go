package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// counterSeedTimeout bounds creating a day's counter outside the caller's
// transaction.
const counterSeedTimeout = 5 * time.Second

// NextDocNo is the next document number of kind prefix on a business day:
// PREFIX-YYMMDD-NNN, counting from 001 each day. day must already be in the
// shop's timezone. In a transaction context the number is taken in the
// transaction, so an aborted command gives its number back.
//
// The day's counter document is created first, outside any transaction:
// two transactions that both insert it (the first bills of a day sold at
// once) do not reliably conflict, and could take the same number or fail.
// Incrementing an existing document always conflicts, and the driver
// retries the losing transaction.
func (m *MongoDB) NextDocNo(ctx context.Context, prefix string, day time.Time) (string, error) {
	key := day.Format("060102")
	id := prefix + "-" + key
	if err := m.seedCounter(id); err != nil {
		return "", fmt.Errorf("%s number: %w", prefix, err)
	}
	var counter struct {
		Seq int `bson:"seq"`
	}
	if err := m.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": id},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter); err != nil {
		return "", fmt.Errorf("%s number: %w", prefix, err)
	}
	return fmt.Sprintf("%s-%s-%03d", prefix, key, counter.Seq), nil
}

// seedCounter makes sure counter id exists (at 0), outside any session.
func (m *MongoDB) seedCounter(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), counterSeedTimeout)
	defer cancel()
	_, err := m.Counters().UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$setOnInsert": bson.M{"seq": 0}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil && !IsDuplicateKey(err) {
		return err
	}
	return nil
}
