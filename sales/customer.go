package sales

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// A customer's spend and visits derive from Sales (CONTEXT-MAP): each
// commercial command has one effect on the customer's account, applied in
// the command's transaction.

// accountEffect is what one command does to a customer's account.
type accountEffect struct {
	spend float64   // added to total_spent (negative takes it back)
	visit time.Time // a new last_visit; zero leaves it as is
}

// saleEffect: the customer spent the bill's total and visited.
func saleEffect(total float64, at time.Time) accountEffect {
	return accountEffect{spend: total, visit: at}
}

// returnEffect: the refund is no longer spent.
func returnEffect(refund float64) accountEffect {
	return accountEffect{spend: -refund}
}

// voidEffect: what is left of the bill after earlier refunds is no longer
// spent; a bill already refunded in full leaves the spend as is.
func voidEffect(total, refunded float64) accountEffect {
	return accountEffect{spend: -max(total-refunded, 0)}
}

func (e accountEffect) none() bool { return e.spend == 0 && e.visit.IsZero() }

// applyToCustomer records e on the customer's account. A sale without a
// customer, or an effect of nothing, touches nothing; a customer that no
// longer exists is mongo.ErrNoDocuments.
func applyToCustomer(ctx context.Context, mdb *db.MongoDB, customerID *bson.ObjectID, e accountEffect) error {
	if customerID == nil || e.none() {
		return nil
	}
	update := bson.M{"$inc": bson.M{"total_spent": e.spend}}
	if !e.visit.IsZero() {
		update["$set"] = bson.M{"last_visit": e.visit}
	}
	res, err := mdb.Customers().UpdateOne(ctx, bson.M{"_id": customerID}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// CustomerSales is a customer's most recent bills, newest first.
func CustomerSales(ctx context.Context, mdb *db.MongoDB, customerID bson.ObjectID, limit int64) ([]models.Sale, error) {
	cur, err := mdb.Sales().Find(ctx,
		bson.M{"customer_id": customerID},
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, err
	}
	sales := []models.Sale{}
	if err := cur.All(ctx, &sales); err != nil {
		return nil, err
	}
	return sales, nil
}
