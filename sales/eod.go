package sales

import (
	"context"
	"errors"
	"time"

	"github.com/app-devper/um-api/sessionclient"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/reporting"
)

// End-of-day close (ADR-0003, ADR-0006). A business day is a calendar date in
// the pharmacy's timezone. Sales count on the day they were confirmed, a void
// on its bill's day, and a return on the day it was made, the same rules the
// live report uses, so a closed day's snapshot plus its adjustments always
// equals what the live report would now show.

const dayLayout = "2006-01-02"

func businessDay(t time.Time, tz *time.Location) string { return t.In(tz).Format(dayLayout) }

// actor is the verified user behind the request, for audit fields.
func actor(ctx context.Context) string {
	p, _ := sessionclient.PrincipalFrom(ctx)
	return p.UserID
}

// Close records the End-of-day close of date (YYYY-MM-DD, or today when
// empty) with the report as it stands. Closing a day again returns the
// existing close (replayed); a future day is refused. closedByName is shown on
// the close receipt; the audit identity is the verified user.
func Close(ctx context.Context, mdb *db.MongoDB, date, closedByName string) (models.EodClose, bool, error) {
	tz := mdb.Timezone(ctx)
	today := businessDay(time.Now(), tz)
	if date == "" {
		date = today
	}
	day, err := time.ParseInLocation(dayLayout, date, tz)
	if err != nil {
		return models.EodClose{}, false, invalid("date must be YYYY-MM-DD")
	}
	date = day.Format(dayLayout)
	if date > today {
		return models.EodClose{}, false, invalid("cannot close a future day")
	}

	find := func(ctx context.Context) (models.EodClose, string, error) {
		var c models.EodClose
		err := mdb.EodCloses().FindOne(ctx, bson.M{"date": date}).Decode(&c)
		return c, "", err
	}
	// The business day itself is the command's identity.
	return runOnce(ctx, date, "", find, func() (models.EodClose, error) {
		userID := actor(ctx)
		closedBy := closedByName
		if closedBy == "" {
			closedBy = userID
		}
		var closed models.EodClose
		err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
			if err := touchDay(txCtx, mdb, date); err != nil {
				return err
			}
			report, err := liveReport(txCtx, mdb, day, tz)
			if err != nil {
				return err
			}
			closed = models.EodClose{
				Date:           date,
				ClosedBy:       closedBy,
				ClosedByUserID: userID,
				ClosedAt:       time.Now(),
				Report:         report,
			}
			res, err := mdb.EodCloses().InsertOne(txCtx, closed)
			if err != nil {
				return err
			}
			closed.ID = res.InsertedID.(bson.ObjectID)
			return nil
		})
		return closed, err
	})
}

// Day is the End-of-day view of date (YYYY-MM-DD, or today when empty): the
// live report for an open day; for a closed day the report as closed, with
// the close and its adjustments.
func Day(ctx context.Context, mdb *db.MongoDB, date string) (models.EodDay, error) {
	tz := mdb.Timezone(ctx)
	if date == "" {
		date = businessDay(time.Now(), tz)
	}
	day, err := time.ParseInLocation(dayLayout, date, tz)
	if err != nil {
		return models.EodDay{}, invalid("date must be YYYY-MM-DD")
	}
	date = day.Format(dayLayout)

	var closed models.EodClose
	err = mdb.EodCloses().FindOne(ctx, bson.M{"date": date}).Decode(&closed)
	if errors.Is(err, mongo.ErrNoDocuments) {
		report, err := liveReport(ctx, mdb, day, tz)
		return models.EodDay{EodReport: report}, err
	}
	if err != nil {
		return models.EodDay{}, err
	}

	cur, err := mdb.EodAdjustments().Find(ctx, bson.M{"date": date}, options.Find().SetSort(bson.D{{Key: "at", Value: 1}}))
	if err != nil {
		return models.EodDay{}, err
	}
	var items []models.EodAdjustment
	if err := cur.All(ctx, &items); err != nil {
		return models.EodDay{}, err
	}
	summary := models.EodAdjustmentSummary{
		Items:              append([]models.EodAdjustment{}, items...),
		AdjustedBillCount:  closed.Report.BillCount,
		AdjustedTotalSales: closed.Report.TotalSales,
		AdjustedNetCash:    closed.Report.NetCash,
	}
	for _, a := range items {
		summary.AdjustedBillCount += a.BillDelta
		summary.AdjustedTotalSales += a.SalesDelta
		summary.AdjustedNetCash += a.CashDelta
	}
	return models.EodDay{
		EodReport: closed.Report,
		Close: &models.EodCloseInfo{
			ID: closed.ID, ClosedBy: closed.ClosedBy, ClosedByUserID: closed.ClosedByUserID, ClosedAt: closed.ClosedAt,
		},
		Adjustments: &summary,
	}, nil
}

// liveReport is the day's report by the reporting rules (ADR-0008).
func liveReport(ctx context.Context, mdb *db.MongoDB, day time.Time, tz *time.Location) (models.EodReport, error) {
	return reporting.Day(ctx, mdb, day, tz)
}

// touchDay writes the day's guard document, so this transaction conflicts
// with any other that affects the same day, including its close.
func touchDay(txCtx context.Context, mdb *db.MongoDB, date string) error {
	_, err := mdb.EodDays().UpdateOne(txCtx,
		bson.M{"_id": date},
		bson.M{"$inc": bson.M{"writes": 1}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// recordDayEffect is called inside a sale, void, or return transaction for
// the business day it affects. If that day is already closed it records adj
// as a Late sale adjustment; either way it guards the day against a
// concurrent close.
func recordDayEffect(txCtx context.Context, mdb *db.MongoDB, adj models.EodAdjustment) error {
	if err := touchDay(txCtx, mdb, adj.Date); err != nil {
		return err
	}
	err := mdb.EodCloses().FindOne(txCtx, bson.M{"date": adj.Date},
		options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	adj.By = actor(txCtx)
	adj.At = time.Now()
	_, err = mdb.EodAdjustments().InsertOne(txCtx, adj)
	return err
}
