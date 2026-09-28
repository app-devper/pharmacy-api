package models

// How a sale met its KY obligations (ADR-0011).
const (
	KyNone           = "none"            // no line needs a KY form
	KyRecorded       = "recorded"        // the forms were recorded with the sale
	KySkippedCashier = "skipped_cashier" // the cashier chose not to record them
	KySkippedSetting = "skipped_setting" // the shop's settings skip KY recording
	KySeparate       = "separate"        // a client that still sends KY forms on their own
)

// SaleKyCapture is what the cashier captured for a sale's KY forms: the
// people and prescription. Drug, quantity, unit, value, date and balance
// come from the recorded sale.
type SaleKyCapture struct {
	Ky10 *Ky10Capture `json:"ky10,omitempty"`
	Ky11 *Ky11Capture `json:"ky11,omitempty"`
	Ky12 *Ky12Capture `json:"ky12,omitempty"`
}

// Ky10Capture — ขย.10, controlled drugs.
type Ky10Capture struct {
	BuyerName    string `json:"buyer_name"`
	BuyerAddress string `json:"buyer_address"` // default: settings ky.default_buyer_address
	RxNo         string `json:"rx_no"`
	Doctor       string `json:"doctor"`
}

// Ky11Capture — ขย.11, dangerous drugs.
type Ky11Capture struct {
	BuyerName  string `json:"buyer_name"`
	Purpose    string `json:"purpose"`
	Pharmacist string `json:"pharmacist"` // default: settings pharmacist.name
}

// Ky12Capture — ขย.12, prescription drugs.
type Ky12Capture struct {
	RxNo        string `json:"rx_no"`
	PatientName string `json:"patient_name"`
	Doctor      string `json:"doctor"`
	Hospital    string `json:"hospital"`
	Status      string `json:"status"` // default "จ่ายแล้ว"
}
