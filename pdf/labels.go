package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/jung-kurt/gofpdf/v2"

	"pharmacy-pos/backend/models"
)

type labelCell struct {
	DrugName     string
	LotNumber    string
	Barcode      string
	Price        float64
	IncludePrice bool
}

// BuildLabelsPDF renders one label per copy across an A4 sheet using the
// label size from `input.Size`. Labels flow left-to-right then top-to-bottom.
// Returns the encoded PDF bytes.
func BuildLabelsPDF(input models.PrintLabelsInput) ([]byte, error) {
	size := models.LabelSizeFromWire(input.Size)
	cells := expandLines(input.Lines)
	if len(cells) == 0 {
		return nil, fmt.Errorf("no labels to print")
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("Sarabun", "", sarabunRegular)
	pdf.AddUTF8FontFromBytes("Sarabun", "B", sarabunBold)
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)

	pageW, pageH := pdf.GetPageSize()
	const marginMm = 5.0
	const gapMm = 2.0

	cols := int(math.Floor((pageW - 2*marginMm + gapMm) / (size.WidthMm + gapMm)))
	rows := int(math.Floor((pageH - 2*marginMm + gapMm) / (size.HeightMm + gapMm)))
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	perPage := cols * rows

	for i, cell := range cells {
		if i%perPage == 0 {
			pdf.AddPage()
		}
		pageIdx := i % perPage
		col := pageIdx % cols
		row := pageIdx / cols
		x := marginMm + float64(col)*(size.WidthMm+gapMm)
		y := marginMm + float64(row)*(size.HeightMm+gapMm)
		if err := drawLabel(pdf, x, y, size, cell); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func expandLines(lines []models.LabelLineInput) []labelCell {
	out := make([]labelCell, 0)
	for _, l := range lines {
		copies := l.Copies
		if copies <= 0 {
			continue
		}
		if copies > 500 {
			copies = 500
		}
		for n := 0; n < copies; n++ {
			out = append(out, labelCell{
				DrugName:     l.DrugName,
				LotNumber:    l.LotNumber,
				Barcode:      strings.TrimSpace(l.Barcode),
				Price:        l.Price,
				IncludePrice: l.IncludePrice,
			})
		}
	}
	return out
}

func drawLabel(pdf *gofpdf.Fpdf, x, y float64, size models.LabelSize, cell labelCell) error {
	pdf.SetDrawColor(220, 220, 220)
	pdf.SetLineWidth(0.1)
	pdf.Rect(x, y, size.WidthMm, size.HeightMm, "D")

	innerMargin := 1.2
	contentX := x + innerMargin
	contentY := y + innerMargin
	contentW := size.WidthMm - 2*innerMargin

	pdf.SetFont("Sarabun", "B", 7)
	pdf.SetXY(contentX, contentY)
	pdf.SetTextColor(34, 34, 34)
	pdf.MultiCell(contentW, 2.6, truncate(cell.DrugName, 60), "", "L", false)

	currentY := pdf.GetY()
	priceLineH := 0.0
	if cell.IncludePrice {
		pdf.SetFont("Sarabun", "B", 9)
		pdf.SetTextColor(37, 99, 235)
		priceLineH = 3.4
		pdf.SetXY(contentX, currentY+0.4)
		pdf.CellFormat(contentW, priceLineH, fmt.Sprintf("฿%.2f", cell.Price), "", 0, "L", false, 0, "")
		currentY += priceLineH + 0.4
	}

	barcodeText := cell.Barcode
	barcodeBottomLineH := 2.4
	barcodeY := y + size.HeightMm - innerMargin - barcodeBottomLineH
	barcodeRegionTop := currentY + 0.6
	barcodeRegionH := barcodeY - barcodeRegionTop
	if barcodeRegionH < 3.0 {
		barcodeRegionH = 3.0
		barcodeRegionTop = barcodeY - barcodeRegionH
	}

	if barcodeText != "" {
		if err := embedCode128(pdf, contentX, barcodeRegionTop, contentW, barcodeRegionH, barcodeText); err != nil {
			return err
		}
	}

	pdf.SetFont("Sarabun", "", 6)
	pdf.SetTextColor(120, 120, 120)
	pdf.SetXY(contentX, barcodeY)
	pdf.CellFormat(contentW, barcodeBottomLineH, barcodeText, "", 0, "C", false, 0, "")

	return nil
}

func embedCode128(pdf *gofpdf.Fpdf, x, y, w, h float64, data string) error {
	code, err := code128.Encode(data)
	if err != nil {
		return fmt.Errorf("code128 encode: %w", err)
	}
	scaled, err := barcode.Scale(code, 600, 100)
	if err != nil {
		return fmt.Errorf("barcode scale: %w", err)
	}
	rgba := toEightBitRGBA(scaled)
	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return fmt.Errorf("png encode: %w", err)
	}
	imageName := "barcode_" + data
	pdf.RegisterImageOptionsReader(
		imageName,
		gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: false},
		bytes.NewReader(buf.Bytes()),
	)
	pdf.ImageOptions(imageName, x, y, w, h, false, gofpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	return nil
}

func toEightBitRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Over)
	return dst
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
