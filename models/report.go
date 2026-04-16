package models

type ReportSummary struct {
	TodaySales float64 `json:"today_sales"`
	TodayBills int     `json:"today_bills"`
	MonthSales float64 `json:"month_sales"`
	StockValue float64 `json:"stock_value"`
	LowStock   int     `json:"low_stock"`
	OutStock   int     `json:"out_stock"`
}

type DailyData struct {
	Day   string  `json:"day"`
	Total float64 `json:"total"`
}

// TopDrug — best-selling drug entry for the top-drugs report
type TopDrug struct {
	DrugID   string  `bson:"_id"       json:"drug_id"`
	DrugName string  `bson:"drug_name" json:"drug_name"`
	QtySold  int     `bson:"qty_sold"  json:"qty_sold"`
	Revenue  float64 `bson:"revenue"   json:"revenue"`
}

// SlowDrug — drug with stock > 0 and no sales in the query window
type SlowDrug struct {
	DrugID   string `bson:"_id"       json:"drug_id"`
	DrugName string `bson:"name"      json:"drug_name"`
	Stock    int    `bson:"stock"     json:"stock"`
	Unit     string `bson:"unit"      json:"unit"`
}

// MonthlyData — revenue vs cost for a single calendar month
type MonthlyData struct {
	Month   string  `bson:"month"   json:"month"`   // "YYYY-MM"
	Revenue float64 `bson:"revenue" json:"revenue"`
	Cost    float64 `bson:"cost"    json:"cost"`
	Profit  float64 `bson:"profit"  json:"profit"`
}

// EodReport — End-of-Day cash reconciliation summary
type EodReport struct {
	Date          string  `json:"date"`           // YYYY-MM-DD
	BillCount     int     `json:"bill_count"`
	TotalSales    float64 `json:"total_sales"`    // sum of sale.total (after discount)
	TotalDiscount float64 `json:"total_discount"` // sum of sale.discount
	TotalReceived float64 `json:"total_received"` // sum of sale.received
	TotalChange   float64 `json:"total_change"`   // sum of sale.change
	NetCash       float64 `json:"net_cash"`       // = total_received - total_change (should equal total_sales)
	Bills         []Sale  `json:"bills"`
}
