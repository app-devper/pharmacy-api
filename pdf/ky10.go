package pdf

import (
	"bytes"
	"fmt"

	"pharmacy-pos/backend/models"
)

func GenerateKy10(rows []models.Ky10, month string) (*bytes.Buffer, error) {
	pdf := NewDoc()
	DrawHeader(pdf, "ขย.10 บัญชีการขายยาควบคุมพิเศษ", month)

	headers := []string{"#", "วันที่", "ชื่อยา", "ทะเบียน", "จำนวน", "หน่วย", "ชื่อผู้ซื้อ", "ที่อยู่", "เลขใบสั่ง", "แพทย์", "คงเหลือ"}
	widths := []float64{7, 17, 28, 18, 11, 10, 28, 30, 18, 22, 12}

	var data [][]string
	for i, r := range rows {
		data = append(data, []string{
			fmt.Sprintf("%d", i+1), r.Date, r.DrugName, r.RegNo,
			fmt.Sprintf("%d", r.Qty), r.Unit, r.BuyerName, r.BuyerAddress,
			r.RxNo, r.Doctor, fmt.Sprintf("%d", r.Balance),
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
