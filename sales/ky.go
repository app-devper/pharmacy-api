package sales

import (
	"context"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// A sale's KY obligations (ADR-0011). Which lines need which form comes from
// each drug's report types. The cashier captures only the people and the
// prescription; the sale supplies the rest: the drug, the quantity and unit
// sold, what the customer paid for the line, the business day, and (ขย.10)
// the stock left after the sale. The forms are recorded in the sale's
// transaction, so a sale and its forms are recorded together, once.

const (
	form10 = "ky10"
	form11 = "ky11"
	form12 = "ky12"
)

// kyPlan is what Sell will do about KY for a sale.
type kyPlan struct {
	status  string
	needs   map[string][]int // form → indexes of the lines that need it
	capture models.SaleKyCapture
}

func kyNeeds(items []preparedSaleItem) map[string][]int {
	needs := map[string][]int{}
	for i, item := range items {
		for _, t := range item.Drug.ReportTypes {
			switch f := strings.ToLower(strings.TrimSpace(t)); f {
			case form10, form11, form12:
				needs[f] = append(needs[f], i)
			}
		}
	}
	return needs
}

// planKy decides the sale's KY status and checks the capture before anything
// is written. Defaults for empty captured fields come from the shop settings.
func planKy(ctx context.Context, mdb *db.MongoDB, input models.SaleInput, items []preparedSaleItem) (kyPlan, error) {
	plan := kyPlan{needs: kyNeeds(items)}
	switch {
	case len(plan.needs) == 0:
		plan.status = models.KyNone
		return plan, nil
	case input.KySkippedByCashier:
		plan.status = models.KySkippedCashier
		return plan, nil
	}
	settings := loadSettings(ctx, mdb)
	switch {
	case settings.KY.SkipAuto:
		plan.status = models.KySkippedSetting
		return plan, nil
	case input.Ky == nil:
		plan.status = models.KySeparate
		return plan, nil
	}
	plan.status = models.KyRecorded
	plan.capture = trimCapture(*input.Ky, settings)
	return plan, plan.check()
}

func trimCapture(c models.SaleKyCapture, s models.Settings) models.SaleKyCapture {
	t := strings.TrimSpace
	if c.Ky10 != nil {
		v := *c.Ky10
		v.BuyerName, v.BuyerAddress, v.RxNo, v.Doctor = t(v.BuyerName), t(v.BuyerAddress), t(v.RxNo), t(v.Doctor)
		if v.BuyerAddress == "" {
			v.BuyerAddress = t(s.KY.DefaultBuyerAddress)
		}
		c.Ky10 = &v
	}
	if c.Ky11 != nil {
		v := *c.Ky11
		v.BuyerName, v.Purpose, v.Pharmacist = t(v.BuyerName), t(v.Purpose), t(v.Pharmacist)
		if v.Pharmacist == "" {
			v.Pharmacist = t(s.Pharmacist.Name)
		}
		c.Ky11 = &v
	}
	if c.Ky12 != nil {
		v := *c.Ky12
		v.RxNo, v.PatientName, v.Doctor, v.Hospital, v.Status = t(v.RxNo), t(v.PatientName), t(v.Doctor), t(v.Hospital), t(v.Status)
		if v.Status == "" {
			v.Status = "จ่ายแล้ว"
		}
		c.Ky12 = &v
	}
	return c
}

// check refuses a capture that lacks a form the sale needs or a field the
// register requires.
func (p kyPlan) check() error {
	missing := func(form, field string) error { return invalid("ขย." + form[2:] + ": " + field + " is required") }
	if len(p.needs[form10]) > 0 {
		c := p.capture.Ky10
		switch {
		case c == nil || c.BuyerName == "":
			return missing(form10, "buyer_name")
		case c.BuyerAddress == "":
			return missing(form10, "buyer_address")
		}
	}
	if len(p.needs[form11]) > 0 {
		c := p.capture.Ky11
		switch {
		case c == nil || c.BuyerName == "":
			return missing(form11, "buyer_name")
		case c.Purpose == "":
			return missing(form11, "purpose")
		case c.Pharmacist == "":
			return missing(form11, "pharmacist")
		}
	}
	if len(p.needs[form12]) > 0 {
		c := p.capture.Ky12
		switch {
		case c == nil || c.RxNo == "":
			return missing(form12, "rx_no")
		case c.PatientName == "":
			return missing(form12, "patient_name")
		case c.Doctor == "":
			return missing(form12, "doctor")
		}
	}
	return nil
}

// soldLine is a sale line as a register records it.
type soldLine struct {
	name, regNo, unit string
	qty               int     // in the unit sold
	paid              float64 // what the customer paid for the line
}

func registerLine(item preparedSaleItem, share float64) soldLine {
	unit, factor := item.Unit, item.UnitFactor
	if unit == "" || factor < 1 {
		unit, factor = item.Drug.Unit, 1
	}
	return soldLine{
		name: item.Drug.Name, regNo: item.Drug.RegNo, unit: unit,
		qty:  item.Qty / factor,
		paid: math.Round(item.Subtotal*share*100) / 100,
	}
}

// recordKy writes the sale's KY forms inside its transaction, after its
// lines took their stock.
func recordKy(txCtx context.Context, mdb *db.MongoDB, plan kyPlan, saleID bson.ObjectID, day string, items []preparedSaleItem, share float64) error {
	if plan.status != models.KyRecorded {
		return nil
	}
	sale := saleID.Hex()
	now := time.Now()
	for _, i := range plan.needs[form10] {
		l := registerLine(items[i], share)
		balance, err := stockOf(txCtx, mdb, items[i].DrugID)
		if err != nil {
			return err
		}
		c := plan.capture.Ky10
		if _, err := mdb.Ky10().InsertOne(txCtx, models.Ky10{
			SaleID: sale, Date: day, DrugName: l.name, RegNo: l.regNo, Qty: l.qty, Unit: l.unit,
			BuyerName: c.BuyerName, BuyerAddress: c.BuyerAddress, RxNo: c.RxNo, Doctor: c.Doctor,
			Balance: max(balance, 0), CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	for _, i := range plan.needs[form11] {
		l := registerLine(items[i], share)
		c := plan.capture.Ky11
		if _, err := mdb.Ky11().InsertOne(txCtx, models.Ky11{
			SaleID: sale, Date: day, DrugName: l.name, RegNo: l.regNo, Qty: l.qty, Unit: l.unit,
			BuyerName: c.BuyerName, Purpose: c.Purpose, Pharmacist: c.Pharmacist, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	for _, i := range plan.needs[form12] {
		l := registerLine(items[i], share)
		c := plan.capture.Ky12
		if _, err := mdb.Ky12().InsertOne(txCtx, models.Ky12{
			SaleID: sale, Date: day, RxNo: c.RxNo, PatientName: c.PatientName, Doctor: c.Doctor,
			Hospital: c.Hospital, DrugName: l.name, Qty: l.qty, Unit: l.unit, TotalValue: l.paid,
			Status: c.Status, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func stockOf(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID) (int, error) {
	var d struct {
		Stock int `bson:"stock"`
	}
	err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&d)
	return d.Stock, err
}

func loadSettings(ctx context.Context, mdb *db.MongoDB) models.Settings {
	var s models.Settings
	if err := mdb.Settings().FindOne(ctx, bson.M{"key": db.SettingsKey}).Decode(&s); err != nil {
		return models.DefaultSettings()
	}
	return s
}
