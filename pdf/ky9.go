package pdf

import (
	"bytes"
	"fmt"

	"pharmacy-pos/backend/models"
)

func GenerateKy9(rows []models.Ky9, month string) (*bytes.Buffer, error) {
	pdf := NewDoc()
	DrawHeader(pdf, "ขย.9 บัญชีการซื้อยาและผลิตภัณฑ์สุขภาพ", month)

	headers := []string{"#", "วันที่", "ชื่อยา", "ทะเบียนยา", "หน่วย", "จำนวน", "ราคา/หน่วย", "มูลค่า", "ผู้ขาย/บริษัท", "เลขใบส่งของ"}
	widths := []float64{8, 18, 38, 20, 12, 14, 16, 20, 35, 22}

	var data [][]string
	var totalVal float64
	for i, r := range rows {
		data = append(data, []string{
			fmt.Sprintf("%d", i+1), r.Date, r.DrugName, r.RegNo, r.Unit,
			fmt.Sprintf("%d", r.Qty), fmt.Sprintf("%.2f", r.PricePerUnit),
			fmt.Sprintf("%.2f", r.TotalValue), r.Seller, r.InvoiceNo,
		})
		totalVal += r.TotalValue
	}
	// pad to 22 rows
	for len(data) < 22 {
		data = append(data, make([]string, len(headers)))
	}
	// total row
	totalRow := make([]string, len(headers))
	totalRow[0] = "รวม"
	totalRow[7] = fmt.Sprintf("%.2f", totalVal)
	data = append(data, totalRow)

	DrawTable(pdf, headers, widths, data)
	DrawSignatures(pdf)

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return &buf, err
}
