package handlers

import (
	"testing"
	"time"
)

// A receive date is stored as midnight in the pharmacy's timezone and read
// back from MongoDB in UTC; its KY9 date must still be that day.
func TestLocalDateKeepsTheStoredDayForUTCDecodedTimes(t *testing.T) {
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	stored, _ := time.ParseInLocation("2006-01-02", "2026-09-27", bangkok)
	fromMongo := stored.UTC() // 2026-09-26T17:00:00Z
	if got := localDate(fromMongo, bangkok); got != "2026-09-27" {
		t.Fatalf("got %s, want 2026-09-27", got)
	}
	if fromMongo.Format("2006-01-02") != "2026-09-26" {
		t.Fatal("test premise: formatting the UTC time gives the previous day")
	}
}
