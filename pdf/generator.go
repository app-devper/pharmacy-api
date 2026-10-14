package pdf

import (
	_ "embed"
	"strings"

	"github.com/jung-kurt/gofpdf/v2"
)

//go:embed assets/fonts/Sarabun-Regular.ttf
var sarabunRegular []byte

//go:embed assets/fonts/Sarabun-Bold.ttf
var sarabunBold []byte

// Shop is the pharmacy a register belongs to, as printed at the top of
// every page of its ขย. forms.
type Shop struct {
	Name      string
	LicenseNo string
	Address   string
}

// headerLines are the shop's two header lines: its name, then its licence
// and address (whichever it has set).
func (s Shop) headerLines() (name, details string) {
	name = s.Name
	if name == "" {
		name = "ร้านยา"
	}
	var parts []string
	if s.LicenseNo != "" {
		parts = append(parts, "ใบอนุญาต "+s.LicenseNo)
	}
	if s.Address != "" {
		parts = append(parts, s.Address)
	}
	return name, strings.Join(parts, "  |  ")
}

// NewDoc creates an A4 PDF with Thai font registered
func NewDoc() *gofpdf.Fpdf {
	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("Sarabun", "", sarabunRegular)
	pdf.AddUTF8FontFromBytes("Sarabun", "B", sarabunBold)
	pdf.SetFont("Sarabun", "", 9)
	return pdf
}

// DrawHeader draws the shop header and returns the starting Y for content.
func DrawHeader(pdf *gofpdf.Fpdf, shop Shop, title, month string) float64 {
	pdf.AddPage()
	w, _ := pdf.GetPageSize()
	margins := 10.0

	name, details := shop.headerLines()
	pdf.SetFont("Sarabun", "B", 12)
	pdf.SetXY(margins, 8)
	pdf.CellFormat(w-margins*2, 7, name, "", 1, "C", false, 0, "")

	if details != "" {
		pdf.SetFont("Sarabun", "", 8)
		pdf.SetX(margins)
		pdf.CellFormat(w-margins*2, 5, details, "", 1, "C", false, 0, "")
	}

	// Form title
	pdf.SetFont("Sarabun", "B", 10)
	pdf.SetX(margins)
	pdf.CellFormat(w-margins*2, 6, title, "", 1, "C", false, 0, "")

	// Month
	if month != "" {
		pdf.SetFont("Sarabun", "", 8)
		pdf.SetX(margins)
		pdf.CellFormat(w-margins*2, 5, "เดือน: "+month, "", 1, "C", false, 0, "")
	}

	pdf.Ln(2)
	return pdf.GetY()
}

// DrawTable renders a table with headers and data rows
func DrawTable(pdf *gofpdf.Fpdf, headers []string, colWidths []float64, rows [][]string) {
	margins := 10.0
	lineH := 6.0

	// Header row
	pdf.SetFont("Sarabun", "B", 8)
	pdf.SetFillColor(240, 240, 240)
	pdf.SetX(margins)
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], lineH, h, "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)

	// Data rows
	pdf.SetFont("Sarabun", "", 8)
	pdf.SetFillColor(255, 255, 255)
	for _, row := range rows {
		pdf.SetX(margins)
		for i, cell := range row {
			if i >= len(colWidths) {
				break
			}
			align := "L"
			pdf.CellFormat(colWidths[i], lineH, cell, "1", 0, align, false, 0, "")
		}
		pdf.Ln(-1)
	}
}

// DrawSignatures draws accountant and pharmacist signature lines
func DrawSignatures(pdf *gofpdf.Fpdf) {
	_, pageH := pdf.GetPageSize()
	y := pageH - 25.0
	w, _ := pdf.GetPageSize()
	margins := 10.0
	halfW := (w - margins*2) / 2

	pdf.SetFont("Sarabun", "", 8)
	pdf.SetXY(margins, y)
	pdf.CellFormat(halfW, 6, "ลงชื่อ ................................ ผู้จัดทำบัญชี", "", 0, "C", false, 0, "")
	pdf.CellFormat(halfW, 6, "ลงชื่อ ................................ เภสัชกรผู้มีหน้าที่ปฏิบัติการ", "", 1, "C", false, 0, "")
}
