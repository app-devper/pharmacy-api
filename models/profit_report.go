package models

type ProfitReport struct {
	Summary ProfitSummary `json:"summary"`
	ByDrug  []DrugProfit  `json:"by_drug"`
}

type ProfitSummary struct {
	Revenue float64 `json:"revenue"`
	Cost    float64 `json:"cost"`
	Profit  float64 `json:"profit"`
	Margin  float64 `json:"margin"` // percentage 0-100
	Bills   int     `json:"bills"`
}

type DrugProfit struct {
	DrugID   string  `json:"drug_id"   bson:"_id"`
	DrugName string  `json:"drug_name" bson:"drug_name"`
	QtySold  int     `json:"qty_sold"  bson:"qty_sold"`
	Revenue  float64 `json:"revenue"   bson:"revenue"`
	Cost     float64 `json:"cost"      bson:"cost"`
	Profit   float64 `json:"profit"    bson:"profit"`
	Margin   float64 `json:"margin"    bson:"margin"`
}
