package pdf

import (
	"bytes"
	"fmt"

	"pharmacy-pos/backend/models"
)

func GenerateKy11(rows []models.Ky11, month string) (*bytes.Buffer, error) {
	pdf := NewDoc()
	DrawHeader(pdf, "ขย.11 บัญชีการขายยาอันตราย", month)

	headers := []string{"#", "วันที่", "ชื่อยา", "ทะเบียนยา", "จำนวน", "หน่วย", "ชื่อผู้รับ", "วัตถุประสงค์", "เภสัชกรผู้จ่าย"}
	widths := []float64{8, 17, 36, 20, 14, 12, 32, 38, 26}

	var data [][]string
	for i, r := range rows {
		data = append(data, []string{
			fmt.Sprintf("%d", i+1), r.Date, r.DrugName, r.RegNo,
			fmt.Sprintf("%d", r.Qty), r.Unit, r.BuyerName, r.Purpose, r.Pharmacist,
		})
	}
	for len(data) < 22 {
		data = append(data, make([]string, len(headers)))
	}

	DrawTable(pdf, headers, widths, data)
	DrawSignatures(pdf)

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return &buf, err
}
