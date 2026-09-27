package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// localDate is t's calendar date (YYYY-MM-DD) in the pharmacy's timezone.
// Times come back from MongoDB in UTC, so formatting them directly shifts any
// time before 07:00 in Bangkok to the previous day.
func localDate(t time.Time, tz *time.Location) string {
	return t.In(tz).Format("2006-01-02")
}
