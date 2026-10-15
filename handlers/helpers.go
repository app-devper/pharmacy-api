package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"pharmacy-pos/backend/calendar"
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
	return calendar.Day(t, tz)
}

// Query numbers every list shares: history lists answer 200 rows unless
// asked, and never more than 1000, so one request cannot read a whole
// collection.
const (
	defaultHistoryLimit = 200
	maxHistoryLimit     = 1000
)

// intParam is ?key= as a positive int, def when absent, malformed, not
// positive, or above max (max 0 means no upper bound).
func intParam(r *http.Request, key string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v <= 0 || (max > 0 && v > max) {
		return def
	}
	return v
}
