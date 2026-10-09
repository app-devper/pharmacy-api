package compliance

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// Recording rows. ctx may be a caller's transaction: the row is written in
// it. A row that breaks its register's rule is refused (refusal.Invalid) and
// nothing is written.

// RecordKy9 records a purchase (ขย.9): a confirmed goods receipt line or a
// manual entry.
func RecordKy9(ctx context.Context, mdb *db.MongoDB, in models.Ky9Input, now time.Time) (models.Ky9, error) {
	t := strings.TrimSpace
	row := models.Ky9{
		SaleID: t(in.SaleID), Date: t(in.Date), DrugName: t(in.DrugName), RegNo: t(in.RegNo), Unit: t(in.Unit),
		Qty: in.Qty, PricePerUnit: in.PricePerUnit, TotalValue: in.PricePerUnit * float64(in.Qty),
		Seller: t(in.Seller), InvoiceNo: t(in.InvoiceNo), CreatedAt: now,
	}
	if err := checkLine(Ky9, row.Date, row.DrugName, row.Qty); err != nil {
		return models.Ky9{}, err
	}
	if row.PricePerUnit < 0 {
		return models.Ky9{}, refuse(Ky9, "price_per_unit", "must be >= 0")
	}
	return insert(ctx, mdb, row)
}

// RecordKy10 records a manual controlled-drug sale row (ขย.10).
func RecordKy10(ctx context.Context, mdb *db.MongoDB, in models.Ky10Input, d Defaults, now time.Time) (models.Ky10, error) {
	t := strings.TrimSpace
	c := fillKy10(models.Ky10Capture{BuyerName: in.BuyerName, BuyerAddress: in.BuyerAddress, RxNo: in.RxNo, Doctor: in.Doctor}, d)
	row := ky10Row(t(in.SaleID), t(in.Date), SaleLine{DrugName: t(in.DrugName), RegNo: t(in.RegNo), Unit: t(in.Unit), Qty: in.Qty, Balance: in.Balance}, c, now)
	if err := checkLine(Ky10, row.Date, row.DrugName, row.Qty); err != nil {
		return models.Ky10{}, err
	}
	if err := checkKy10People(c); err != nil {
		return models.Ky10{}, err
	}
	if row.Balance < 0 {
		return models.Ky10{}, refuse(Ky10, "balance", "must be >= 0")
	}
	return insert(ctx, mdb, row)
}

// RecordKy11 records a manual dangerous-drug sale row (ขย.11).
func RecordKy11(ctx context.Context, mdb *db.MongoDB, in models.Ky11Input, d Defaults, now time.Time) (models.Ky11, error) {
	t := strings.TrimSpace
	c := fillKy11(models.Ky11Capture{BuyerName: in.BuyerName, Purpose: in.Purpose, Pharmacist: in.Pharmacist}, d)
	row := ky11Row(t(in.SaleID), t(in.Date), SaleLine{DrugName: t(in.DrugName), RegNo: t(in.RegNo), Unit: t(in.Unit), Qty: in.Qty}, c, now)
	if err := checkLine(Ky11, row.Date, row.DrugName, row.Qty); err != nil {
		return models.Ky11{}, err
	}
	if err := checkKy11People(c); err != nil {
		return models.Ky11{}, err
	}
	return insert(ctx, mdb, row)
}

// RecordKy12 records a manual prescription sale row (ขย.12). total_value is
// what the client says the line cost; only its sign can be checked.
func RecordKy12(ctx context.Context, mdb *db.MongoDB, in models.Ky12Input, now time.Time) (models.Ky12, error) {
	t := strings.TrimSpace
	c := fillKy12(models.Ky12Capture{RxNo: in.RxNo, PatientName: in.PatientName, Doctor: in.Doctor, Hospital: in.Hospital, Status: in.Status})
	row := ky12Row(t(in.SaleID), t(in.Date), SaleLine{DrugName: t(in.DrugName), Unit: t(in.Unit), Qty: in.Qty, Paid: in.TotalValue}, c, now)
	if err := checkLine(Ky12, row.Date, row.DrugName, row.Qty); err != nil {
		return models.Ky12{}, err
	}
	if err := checkKy12People(c); err != nil {
		return models.Ky12{}, err
	}
	if row.TotalValue < 0 {
		return models.Ky12{}, refuse(Ky12, "total_value", "must be >= 0")
	}
	return insert(ctx, mdb, row)
}

// SaleLine is a sold line as a register records it.
type SaleLine struct {
	DrugName, RegNo, Unit string
	Qty                   int     // in the unit sold
	Paid                  float64 // what the customer paid for the line (ขย.12)
	Balance               int     // stock left after the sale (ขย.10)
}

// PrepareCapture applies the shop's defaults to a cashier's capture and
// refuses it, before the sale writes anything, when it lacks a form the sale
// needs or a field that form requires.
func PrepareCapture(c models.SaleKyCapture, needs []Form, d Defaults) (models.SaleKyCapture, error) {
	if c.Ky10 != nil {
		v := fillKy10(*c.Ky10, d)
		c.Ky10 = &v
	}
	if c.Ky11 != nil {
		v := fillKy11(*c.Ky11, d)
		c.Ky11 = &v
	}
	if c.Ky12 != nil {
		v := fillKy12(*c.Ky12)
		c.Ky12 = &v
	}
	for _, f := range needs {
		var err error
		switch f {
		case Ky10:
			err = checkKy10People(deref(c.Ky10))
		case Ky11:
			err = checkKy11People(deref(c.Ky11))
		case Ky12:
			err = checkKy12People(deref(c.Ky12))
		}
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func deref[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}

// RecordSale records form's rows for a sale's lines on its business day, in
// the sale's transaction. The capture must have been through PrepareCapture.
func RecordSale(txCtx context.Context, mdb *db.MongoDB, f Form, saleID, day string, c models.SaleKyCapture, lines []SaleLine, now time.Time) error {
	for _, l := range lines {
		var err error
		switch f {
		case Ky10:
			_, err = insert(txCtx, mdb, ky10Row(saleID, day, l, deref(c.Ky10), now))
		case Ky11:
			_, err = insert(txCtx, mdb, ky11Row(saleID, day, l, deref(c.Ky11), now))
		case Ky12:
			_, err = insert(txCtx, mdb, ky12Row(saleID, day, l, deref(c.Ky12), now))
		default:
			err = refusal.Invalidf(string(f) + " is not a sale register")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func ky10Row(saleID, day string, l SaleLine, c models.Ky10Capture, now time.Time) models.Ky10 {
	return models.Ky10{
		SaleID: saleID, Date: day, DrugName: l.DrugName, RegNo: l.RegNo, Qty: l.Qty, Unit: l.Unit,
		BuyerName: c.BuyerName, BuyerAddress: c.BuyerAddress, RxNo: c.RxNo, Doctor: c.Doctor,
		Balance: l.Balance, CreatedAt: now,
	}
}

func ky11Row(saleID, day string, l SaleLine, c models.Ky11Capture, now time.Time) models.Ky11 {
	return models.Ky11{
		SaleID: saleID, Date: day, DrugName: l.DrugName, RegNo: l.RegNo, Qty: l.Qty, Unit: l.Unit,
		BuyerName: c.BuyerName, Purpose: c.Purpose, Pharmacist: c.Pharmacist, CreatedAt: now,
	}
}

func ky12Row(saleID, day string, l SaleLine, c models.Ky12Capture, now time.Time) models.Ky12 {
	return models.Ky12{
		SaleID: saleID, Date: day, RxNo: c.RxNo, PatientName: c.PatientName, Doctor: c.Doctor,
		Hospital: c.Hospital, DrugName: l.DrugName, Qty: l.Qty, Unit: l.Unit, TotalValue: l.Paid,
		Status: c.Status, CreatedAt: now,
	}
}

// insert writes row into its register and returns it with its id.
func insert[T Row](ctx context.Context, mdb *db.MongoDB, row T) (T, error) {
	res, err := collection[T](mdb).InsertOne(ctx, row)
	if err != nil {
		return row, err
	}
	id := res.InsertedID.(bson.ObjectID)
	switch r := any(&row).(type) {
	case *models.Ky9:
		r.ID = id
	case *models.Ky10:
		r.ID = id
	case *models.Ky11:
		r.ID = id
	case *models.Ky12:
		r.ID = id
	}
	return row, nil
}
