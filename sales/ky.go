package sales

import (
	"context"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/compliance"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// A sale's KY obligations (ADR-0011). Which lines need which form comes from
// each drug's report types; that decision is the sale's. The cashier captures
// only the people and the prescription; the sale supplies the rest: the drug,
// the quantity and unit sold, what the customer paid for the line, the
// business day, and (ขย.10) the stock left after the sale. What a valid row
// is and how it is written belong to the compliance module. The forms are
// recorded in the sale's transaction, so a sale and its forms are recorded
// together, once.

// saleForms are the registers a sale records, in the order it records them.
var saleForms = []compliance.Form{compliance.Ky10, compliance.Ky11, compliance.Ky12}

// kyPlan is what Sell will do about KY for a sale.
type kyPlan struct {
	status  string
	needs   map[compliance.Form][]int // form → indexes of the lines that need it
	capture models.SaleKyCapture
}

func kyNeeds(items []preparedSaleItem) map[compliance.Form][]int {
	needs := map[compliance.Form][]int{}
	for i, item := range items {
		for _, t := range item.Drug.ReportTypes {
			if f, ok := compliance.ParseForm(t); ok && f != compliance.Ky9 {
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
	var needed []compliance.Form
	for _, f := range saleForms {
		if len(plan.needs[f]) > 0 {
			needed = append(needed, f)
		}
	}
	var err error
	plan.capture, err = compliance.PrepareCapture(*input.Ky, needed, compliance.DefaultsFrom(settings))
	return plan, err
}

func registerLine(item preparedSaleItem, share float64) compliance.SaleLine {
	unit, factor := item.Unit, item.UnitFactor
	if unit == "" || factor < 1 {
		unit, factor = item.Drug.Unit, 1
	}
	return compliance.SaleLine{
		DrugName: item.Drug.Name, RegNo: item.Drug.RegNo, Unit: unit,
		Qty:  item.Qty / factor,
		Paid: math.Round(item.Subtotal*share*100) / 100,
	}
}

// recordKy writes the sale's KY forms inside its transaction, after its
// lines took their stock.
func recordKy(txCtx context.Context, mdb *db.MongoDB, plan kyPlan, saleID bson.ObjectID, day string, items []preparedSaleItem, share float64) error {
	if plan.status != models.KyRecorded {
		return nil
	}
	now := time.Now()
	for _, f := range saleForms {
		lines := make([]compliance.SaleLine, 0, len(plan.needs[f]))
		for _, i := range plan.needs[f] {
			l := registerLine(items[i], share)
			if f == compliance.Ky10 {
				balance, err := stockOf(txCtx, mdb, items[i].DrugID)
				if err != nil {
					return err
				}
				l.Balance = max(balance, 0)
			}
			lines = append(lines, l)
		}
		if err := compliance.RecordSale(txCtx, mdb, f, saleID.Hex(), day, plan.capture, lines, now); err != nil {
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
