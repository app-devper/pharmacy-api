package sales

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Abandoning a queued sale (ADR-0009). A client that cannot get a queued sale
// accepted records here that the pharmacy will never record it, so the queue
// entry can be closed with an audit trail instead of being deleted. After
// that, selling with the same client request id is refused. A sale and its
// abandonment exclude each other: both write the request's guard document,
// so one of two concurrent attempts sees the other.
//
// A recorded sale whose KY forms were refused is closed the same way with
// kind ky_forms, keeping the forms that will not be recorded.

// AlreadySold refuses abandoning a queued sale that was recorded meanwhile;
// the client marks its entry synced with Sale.
type AlreadySold struct{ Sale models.SaleResponse }

func (e *AlreadySold) Error() string { return "sale was already recorded" }

var errSaleAbandoned = conflict("this queued sale was abandoned and will not be recorded")

// Abandon records an abandonment. Repeating it returns the first record
// (replayed).
func Abandon(ctx context.Context, mdb *db.MongoDB, in models.AbandonInput) (models.Abandonment, bool, error) {
	in.ClientRequestID = strings.TrimSpace(in.ClientRequestID)
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.ClientRequestID == "":
		return models.Abandonment{}, false, invalid("client_request_id is required")
	case in.Kind != models.AbandonSale && in.Kind != models.AbandonKyForms:
		return models.Abandonment{}, false, invalid("kind must be sale or ky_forms")
	case in.Reason == "":
		return models.Abandonment{}, false, invalid("reason is required")
	}
	payload, err := json.Marshal(in.Payload)
	if err != nil {
		return models.Abandonment{}, false, invalid("payload is not JSON")
	}
	record := models.Abandonment{
		ClientRequestID: in.ClientRequestID,
		Kind:            in.Kind,
		Payload:         string(payload),
		Reason:          in.Reason,
		By:              actor(ctx),
		At:              time.Now(),
	}

	var out models.Abandonment
	var replayed bool
	err = mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		out, replayed = models.Abandonment{}, false
		if in.Kind == models.AbandonSale {
			if err := guardRequest(txCtx, mdb, in.ClientRequestID); err != nil {
				return err
			}
		}
		prior, err := findAbandonment(txCtx, mdb, in.Kind, in.ClientRequestID)
		if err == nil {
			out, replayed = prior, true
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		var sale models.Sale
		err = mdb.Sales().FindOne(txCtx, bson.M{"client_request_id": in.ClientRequestID}).Decode(&sale)
		switch {
		case in.Kind == models.AbandonSale && err == nil:
			return &AlreadySold{Sale: saleResponse(sale)}
		case in.Kind == models.AbandonKyForms && errors.Is(err, mongo.ErrNoDocuments):
			return notFound("sale not found for client_request_id")
		case err != nil && !errors.Is(err, mongo.ErrNoDocuments):
			return err
		}
		out = record
		if in.Kind == models.AbandonKyForms {
			out.SaleID, out.BillNo = sale.ID.Hex(), sale.BillNo
		}
		res, err := mdb.Abandonments().InsertOne(txCtx, out)
		if err != nil {
			return err
		}
		out.ID = res.InsertedID.(bson.ObjectID)
		return nil
	})
	if err != nil && db.IsDuplicateKey(err) {
		// A concurrent repeat committed first.
		prior, ferr := findAbandonment(ctx, mdb, in.Kind, in.ClientRequestID)
		if ferr != nil {
			return models.Abandonment{}, false, ferr
		}
		return prior, true, nil
	}
	return out, replayed, err
}

// Abandoned lists abandonments, newest first.
func Abandoned(ctx context.Context, mdb *db.MongoDB, limit int64) ([]models.Abandonment, error) {
	cur, err := mdb.Abandonments().Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	out := []models.Abandonment{}
	err = cur.All(ctx, &out)
	return out, err
}

func findAbandonment(ctx context.Context, mdb *db.MongoDB, kind, requestID string) (models.Abandonment, error) {
	var a models.Abandonment
	err := mdb.Abandonments().FindOne(ctx, bson.M{"kind": kind, "client_request_id": requestID}).Decode(&a)
	return a, err
}

// guardRequest writes the request's guard document inside a transaction, so
// a concurrent sale or abandonment of the same request conflicts and retries.
func guardRequest(txCtx context.Context, mdb *db.MongoDB, requestID string) error {
	_, err := mdb.RequestGuards().UpdateOne(txCtx,
		bson.M{"_id": requestID},
		bson.M{"$set": bson.M{"at": time.Now()}},
		options.UpdateOne().SetUpsert(true))
	return err
}

// refuseAbandoned guards a sale's request id and refuses it when the queued
// sale was abandoned.
func refuseAbandoned(txCtx context.Context, mdb *db.MongoDB, requestID string) error {
	if requestID == "" {
		return nil
	}
	if err := guardRequest(txCtx, mdb, requestID); err != nil {
		return err
	}
	_, err := findAbandonment(txCtx, mdb, models.AbandonSale, requestID)
	switch {
	case err == nil:
		return errSaleAbandoned
	case errors.Is(err, mongo.ErrNoDocuments):
		return nil
	}
	return err
}
