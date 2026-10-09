// Package compliance owns the KY registers (ขย.9–12): what a valid row is,
// the shop's defaults for empty fields, recording a row (inside a caller's
// transaction when it has one), and reading a register by month or by sale.
//
// Rows come from three places, all through here: a cashier's KY capture with
// a sale (sales decides which lines need which form, ADR-0011), a confirmed
// goods receipt (ขย.9, ADR-0012), and manual entry.
package compliance

import (
	"context"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// Form names a KY register.
type Form string

const (
	Ky9  Form = "ky9"
	Ky10 Form = "ky10"
	Ky11 Form = "ky11"
	Ky12 Form = "ky12"
)

// Label is the form's name on paper, e.g. "ขย.10".
func (f Form) Label() string { return "ขย." + strings.TrimPrefix(string(f), "ky") }

// ParseForm is the register a report type or path names, if any.
func ParseForm(s string) (Form, bool) {
	switch f := Form(strings.ToLower(strings.TrimSpace(s))); f {
	case Ky9, Ky10, Ky11, Ky12:
		return f, true
	}
	return "", false
}

// PaidStatus is a ขย.12 row's status when none is given.
const PaidStatus = "จ่ายแล้ว"

const dateLayout = "2006-01-02"

// Defaults are the shop's values for fields a cashier or clerk left empty.
type Defaults struct {
	BuyerAddress string // ขย.10 buyer address
	Pharmacist   string // ขย.11 pharmacist
}

// LoadDefaults reads the shop's defaults from its settings; none when the
// settings cannot be read.
func LoadDefaults(ctx context.Context, mdb *db.MongoDB) Defaults {
	var s models.Settings
	if err := mdb.Settings().FindOne(ctx, bson.M{"key": db.SettingsKey}).Decode(&s); err != nil {
		return Defaults{}
	}
	return DefaultsFrom(s)
}

// DefaultsFrom is the defaults in settings a caller already read.
func DefaultsFrom(s models.Settings) Defaults {
	return Defaults{BuyerAddress: strings.TrimSpace(s.KY.DefaultBuyerAddress), Pharmacist: strings.TrimSpace(s.Pharmacist.Name)}
}

func refuse(f Form, field, rule string) error {
	return refusal.Invalidf(f.Label() + ": " + field + " " + rule)
}

func required(f Form, fields ...[2]string) error {
	for _, fv := range fields {
		if fv[1] == "" {
			return refuse(f, fv[0], "is required")
		}
	}
	return nil
}

// checkLine is the rule every register row shares: a real calendar date,
// the drug, and a positive quantity.
func checkLine(f Form, date, drug string, qty int) error {
	if date == "" {
		return refuse(f, "date", "is required")
	}
	if _, err := time.Parse(dateLayout, date); err != nil {
		return refuse(f, "date", "must be YYYY-MM-DD")
	}
	if drug == "" {
		return refuse(f, "drug_name", "is required")
	}
	if qty <= 0 {
		return refuse(f, "qty", "must be > 0")
	}
	return nil
}

// The people and prescription each sale form needs. A sale's capture and a
// manual entry are checked by the same rule.

func checkKy10People(c models.Ky10Capture) error {
	return required(Ky10, [2]string{"buyer_name", c.BuyerName}, [2]string{"buyer_address", c.BuyerAddress})
}

func checkKy11People(c models.Ky11Capture) error {
	return required(Ky11, [2]string{"buyer_name", c.BuyerName}, [2]string{"purpose", c.Purpose}, [2]string{"pharmacist", c.Pharmacist})
}

func checkKy12People(c models.Ky12Capture) error {
	return required(Ky12, [2]string{"rx_no", c.RxNo}, [2]string{"patient_name", c.PatientName}, [2]string{"doctor", c.Doctor})
}

// The shop's defaults and trimming, applied the same way to a capture and a
// manual entry.

func fillKy10(c models.Ky10Capture, d Defaults) models.Ky10Capture {
	t := strings.TrimSpace
	c.BuyerName, c.BuyerAddress, c.RxNo, c.Doctor = t(c.BuyerName), t(c.BuyerAddress), t(c.RxNo), t(c.Doctor)
	if c.BuyerAddress == "" {
		c.BuyerAddress = d.BuyerAddress
	}
	return c
}

func fillKy11(c models.Ky11Capture, d Defaults) models.Ky11Capture {
	t := strings.TrimSpace
	c.BuyerName, c.Purpose, c.Pharmacist = t(c.BuyerName), t(c.Purpose), t(c.Pharmacist)
	if c.Pharmacist == "" {
		c.Pharmacist = d.Pharmacist
	}
	return c
}

func fillKy12(c models.Ky12Capture) models.Ky12Capture {
	t := strings.TrimSpace
	c.RxNo, c.PatientName, c.Doctor, c.Hospital, c.Status = t(c.RxNo), t(c.PatientName), t(c.Doctor), t(c.Hospital), t(c.Status)
	if c.Status == "" {
		c.Status = PaidStatus
	}
	return c
}

var monthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// monthFilter matches a register's rows dated in month (YYYY-MM); an empty
// month matches every row.
func monthFilter(month string) (bson.M, error) {
	if month == "" {
		return bson.M{}, nil
	}
	if !monthPattern.MatchString(month) {
		return nil, refusal.Invalidf("month must be YYYY-MM")
	}
	return bson.M{"date": bson.M{"$regex": "^" + regexp.QuoteMeta(month) + "-"}}, nil
}
