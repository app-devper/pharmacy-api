package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EodClose is a durable End-of-day close (ADR-0003, ADR-0006): the covered
// business day, who closed it and when, and the report as it stood then. The
// snapshot never changes; later effects on the day are EodAdjustments.
type EodClose struct {
	ID   bson.ObjectID `bson:"_id,omitempty" json:"close_id"`
	Date string        `bson:"date"          json:"date"` // business day, YYYY-MM-DD in the pharmacy's timezone
	// ClosedBy is the name shown on the close receipt (the verified user id
	// when the client sent no name); ClosedByUserID is the audit identity.
	ClosedBy       string    `bson:"closed_by"         json:"closed_by"`
	ClosedByUserID string    `bson:"closed_by_user_id" json:"closed_by_user_id"`
	ClosedAt       time.Time `bson:"closed_at"         json:"closed_at"`
	Report         EodReport `bson:"report"            json:"report"`
}

// Kinds of Late sale adjustment to a closed day.
const (
	AdjustLateSale = "late_sale" // a sale confirmed on the day after it closed
	AdjustVoid     = "void"      // a bill of the closed day voided later
	AdjustReturn   = "return"    // a return made on the day after it closed
)

// EodAdjustment is a Late sale adjustment: an auditable change to a closed
// day's totals, recorded in the same transaction as the sale, void, or return
// that caused it.
type EodAdjustment struct {
	ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Date       string        `bson:"date"          json:"date"`
	Kind       string        `bson:"kind"          json:"kind"`
	RefID      bson.ObjectID `bson:"ref_id"        json:"ref_id"`
	RefNo      string        `bson:"ref_no"        json:"ref_no"` // bill or return number
	BillDelta  int           `bson:"bill_delta"    json:"bill_delta"`
	SalesDelta float64       `bson:"sales_delta"   json:"sales_delta"`    // effect on total_sales
	CashDelta  float64       `bson:"cash_delta"    json:"net_cash_delta"` // effect on net_cash
	By         string        `bson:"by"            json:"by"`             // verified user id
	At         time.Time     `bson:"at"            json:"at"`
}

// EodAdjustmentSummary lists a closed day's adjustments and its totals
// after them.
type EodAdjustmentSummary struct {
	Items              []EodAdjustment `json:"items"`
	AdjustedBillCount  int             `json:"adjusted_bill_count"`
	AdjustedTotalSales float64         `json:"adjusted_total_sales"`
	AdjustedNetCash    float64         `json:"adjusted_net_cash"`
}

// EodCloseInfo is the close shown alongside a closed day's report.
type EodCloseInfo struct {
	ID             bson.ObjectID `json:"close_id"`
	ClosedBy       string        `json:"closed_by"`
	ClosedByUserID string        `json:"closed_by_user_id"`
	ClosedAt       time.Time     `json:"closed_at"`
}

// EodDay is GET /report/eod: the live report for an open day; for a closed
// day the report as closed, with the close and its adjustments.
type EodDay struct {
	EodReport
	Close       *EodCloseInfo         `json:"close,omitempty"`
	Adjustments *EodAdjustmentSummary `json:"adjustments,omitempty"`
}
