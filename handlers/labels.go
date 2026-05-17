package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/pdf"
)

type LabelHandler struct{}

func NewLabelHandler() *LabelHandler { return &LabelHandler{} }

const maxLabelRequestLines = 200

func (h *LabelHandler) Print(w http.ResponseWriter, r *http.Request) {
	var input models.PrintLabelsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(input.Lines) == 0 {
		jsonError(w, "lines is required", http.StatusBadRequest)
		return
	}
	if len(input.Lines) > maxLabelRequestLines {
		jsonError(w, "too many lines", http.StatusBadRequest)
		return
	}
	total := 0
	for _, l := range input.Lines {
		if l.Copies < 0 {
			jsonError(w, "copies must be non-negative", http.StatusBadRequest)
			return
		}
		total += l.Copies
	}
	if total == 0 {
		jsonError(w, "total copies is zero", http.StatusBadRequest)
		return
	}

	bytes, err := pdf.BuildLabelsPDF(input)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filename := "labels_" + time.Now().Format("20060102_150405") + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bytes)
}
