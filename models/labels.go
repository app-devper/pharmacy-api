package models

type LabelSize struct {
	WidthMm  float64 `json:"width_mm"`
	HeightMm float64 `json:"height_mm"`
}

var (
	LabelSizeSmall  = LabelSize{WidthMm: 38, HeightMm: 25}
	LabelSizeMedium = LabelSize{WidthMm: 50, HeightMm: 30}
)

func LabelSizeFromWire(wire string) LabelSize {
	switch wire {
	case "50x30", "medium":
		return LabelSizeMedium
	default:
		return LabelSizeSmall
	}
}

type LabelLineInput struct {
	DrugName     string  `json:"drug_name"`
	LotNumber    string  `json:"lot_number,omitempty"`
	Barcode      string  `json:"barcode"`
	Price        float64 `json:"price,omitempty"`
	IncludePrice bool    `json:"include_price"`
	Copies       int     `json:"copies"`
}

type PrintLabelsInput struct {
	Size  string           `json:"size"`
	Lines []LabelLineInput `json:"lines"`
}
