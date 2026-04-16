package pdf

import (
	"bytes"
	"fmt"

	"pharmacy-pos/backend/models"
)

func GenerateKy12(rows []models.Ky12, month string) (*bytes.Buffer, error) {
	pdf := NewDoc()
	DrawHeader(pdf, "ขย.12 บัญชีการขายยาตามใบสั่งแพทย์", month)

	headers := []string{"#", "วันที่", "เลขใบสั่ง", "ชื่อผู้ป่วย", "แพทย์", "สถานพยาบาล", "รายการยา", "จำนวน", "หน่วย", "มูลค่า"}
	widths := []float64{8, 17, 22, 30, 28, 32, 28, 12, 12, 14}

	var data [][]string
	var totalVal float64
	for i, r := range rows {
		data = append(data, []string{
			fmt.Sprintf("%d", i+1), r.Date, r.RxNo, r.PatientName,
			r.Doctor, r.Hospital, r.DrugName,
			fmt.Sprintf("%d", r.Qty), r.Unit, fmt.Sprintf("%.2f", r.TotalValue),
		})
		totalVal += r.TotalValue
	}
	for len(data) < 22 {
		data = append(data, make([]string, len(headers)))
	}
	totalRow := make([]string, len(headers))
	totalRow[0] = "รวม"
	totalRow[9] = fmt.Sprintf("%.2f", totalVal)
	data = append(data, totalRow)

	DrawTable(pdf, headers, widths, data)
	DrawSignatures(pdf)

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return &buf, err
}
