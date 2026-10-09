package sales

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Sell confirms a Sale: it prices the lines from the catalog, takes stock
// from lots first-expiry-first, numbers the bill, and adds to the customer's
// spend, all in one transaction. With a client_request_id it is a Commercial
// command, and replayed reports that the outcome was recorded earlier.
func Sell(ctx context.Context, mdb *db.MongoDB, input models.SaleInput) (out models.SaleResponse, replayed bool, err error) {
	if len(input.Items) == 0 {
		return out, false, invalid("items is required")
	}
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	request := input
	request.ClientRequestID = ""
	fp := fingerprint(request)

	find := func(ctx context.Context) (models.SaleResponse, string, error) {
		var sale models.Sale
		if err := mdb.Sales().FindOne(ctx, bson.M{"client_request_id": input.ClientRequestID}).Decode(&sale); err != nil {
			return models.SaleResponse{}, "", err
		}
		return saleResponse(sale), sale.RequestFingerprint, nil
	}
	return runOnce(ctx, input.ClientRequestID, fp, find, func() (models.SaleResponse, error) {
		return sell(ctx, mdb, input, fp)
	})
}

func sell(ctx context.Context, mdb *db.MongoDB, input models.SaleInput, fp string) (models.SaleResponse, error) {
	preparedItems, subtotal, err := prepareSaleItems(ctx, mdb, input.Items)
	if err != nil {
		switch {
		case errors.Is(err, bson.ErrInvalidHex):
			return models.SaleResponse{}, invalid("invalid drug id")
		case errors.Is(err, mongo.ErrNoDocuments):
			return models.SaleResponse{}, invalid("drug not found")
		}
		return models.SaleResponse{}, invalid(err.Error())
	}

	customerID, customerName, err := resolveSaleCustomer(ctx, mdb, input.CustomerID)
	if err != nil {
		if errors.Is(err, bson.ErrInvalidHex) || errors.Is(err, mongo.ErrNoDocuments) {
			return models.SaleResponse{}, invalid("customer not found")
		}
		return models.SaleResponse{}, invalid(err.Error())
	}

	discount := math.Max(0, math.Min(input.Discount, subtotal))
	total := subtotal - discount
	received := input.Received
	if received == 0 {
		received = total
	}
	if received < total {
		return models.SaleResponse{}, invalid("received must be >= total")
	}
	change := math.Max(0, received-total)
	share := 1.0 // the part of each line's subtotal the customer paid
	if subtotal > 0 {
		share = total / subtotal
	}
	ky, err := planKy(ctx, mdb, input, preparedItems)
	if err != nil {
		return models.SaleResponse{}, err
	}

	// Bill number is keyed by calendar day in the pharmacy's timezone so same-day
	// sales share one counter and the YYMMDD prefix matches the local date.
	tz := mdb.Timezone(ctx)
	var billNo string
	var saleID bson.ObjectID
	requestFp := ""
	if input.ClientRequestID != "" {
		requestFp = fp
	}
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := refuseAbandoned(txCtx, mdb, input.ClientRequestID); err != nil {
			return err
		}
		now := time.Now().In(tz)
		generatedBillNo, err := mdb.NextDocNo(txCtx, "INV", now)
		if err != nil {
			return err
		}

		sale := models.Sale{
			BillNo:             generatedBillNo,
			ClientRequestID:    input.ClientRequestID,
			RequestFingerprint: requestFp,
			CustomerID:         customerID,
			CustomerName:       customerName,
			Discount:           discount,
			Total:              total,
			Received:           received,
			Change:             change,
			SoldAt:             now,
			KySkippedByCashier: input.KySkippedByCashier,
			KyStatus:           ky.status,
		}
		res, err := mdb.Sales().InsertOne(txCtx, sale)
		if err != nil {
			return err
		}
		saleOID := res.InsertedID.(bson.ObjectID)
		saleID = saleOID

		for _, item := range preparedItems {
			if err := applySaleItem(txCtx, mdb, saleOID, item); err != nil {
				return err
			}
		}

		if customerID != nil {
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": customerID},
				bson.M{
					"$inc": bson.M{"total_spent": total},
					"$set": bson.M{"last_visit": now},
				},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}

		if err := recordKy(txCtx, mdb, ky, saleOID, now.Format(dayLayout), preparedItems, share); err != nil {
			return err
		}

		billNo = generatedBillNo
		return recordDayEffect(txCtx, mdb, models.EodAdjustment{
			Date: now.Format(dayLayout), Kind: models.AdjustLateSale, RefID: saleOID, RefNo: generatedBillNo,
			BillDelta: 1, SalesDelta: total, CashDelta: received - change,
		})
	}); err != nil {
		if db.IsDuplicateKey(err) {
			return models.SaleResponse{}, err // runOnce replays the committed attempt
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.SaleResponse{}, invalid("drug not found")
		}
		return models.SaleResponse{}, err
	}

	// Fresh stock values for each drug sold, so the client can patch local
	// state without fetching the drug list again. One query per unique drug.
	updates := make([]models.StockUpdate, 0, len(preparedItems))
	seen := make(map[bson.ObjectID]struct{}, len(preparedItems))
	for _, it := range preparedItems {
		if _, ok := seen[it.DrugID]; ok {
			continue
		}
		seen[it.DrugID] = struct{}{}
		var d struct {
			Stock int `bson:"stock"`
		}
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": it.DrugID},
			options.FindOne().SetProjection(bson.M{"stock": 1}),
		).Decode(&d); err == nil {
			updates = append(updates, models.StockUpdate{DrugID: it.DrugID, NewStock: d.Stock})
		}
	}

	return models.SaleResponse{
		ID:     saleID.Hex(),
		BillNo: billNo, Discount: discount, Total: total, Change: change,
		StockUpdates:       updates,
		KySkippedByCashier: input.KySkippedByCashier,
		KyStatus:           ky.status,
	}, nil
}

// saleResponse is a recorded sale as a Sell outcome, without stock updates.
func saleResponse(sale models.Sale) models.SaleResponse {
	return models.SaleResponse{
		ID:                 sale.ID.Hex(),
		BillNo:             sale.BillNo,
		Discount:           sale.Discount,
		Total:              sale.Total,
		Change:             sale.Change,
		KySkippedByCashier: sale.KySkippedByCashier,
		KyStatus:           sale.KyStatus,
	}
}
