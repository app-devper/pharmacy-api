package calendar

import (
	"testing"
	"time"
)

func TestDatesCoverWholeDaysInTheShopsTimezone(t *testing.T) {
	bkk, _ := time.LoadLocation("Asia/Bangkok")
	start, end := Dates(bkk, "2026-05-01", "2026-05-31")
	if !start.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, bkk)) || !end.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, bkk)) {
		t.Fatalf("period %s – %s", start, end)
	}
}

func TestADayWithAClockChangeIsCoveredExactly(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zone database")
	}
	// 2026-03-08 is 23 hours long in New York.
	_, end := Dates(ny, "", "2026-03-08")
	if !end.Equal(time.Date(2026, 3, 9, 0, 0, 0, 0, ny)) {
		t.Fatalf("end %s, want the next midnight", end)
	}
}

func TestMissingOrMalformedDatesLeaveTheBoundOpen(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {"2026/05/01", "31-05-2026"}, {"2026-02-30", "x"}} {
		start, end := Dates(time.UTC, c[0], c[1])
		if !start.IsZero() || !end.IsZero() {
			t.Fatalf("%v → %s – %s, want open", c, start, end)
		}
	}
}

func TestBusinessDayAndMonthFollowTheTimezone(t *testing.T) {
	bkk, _ := time.LoadLocation("Asia/Bangkok")
	at := time.Date(2026, 5, 31, 17, 30, 0, 0, time.UTC) // 00:30 on 1 June in Bangkok
	if Day(at, bkk) != "2026-06-01" {
		t.Fatalf("day %s", Day(at, bkk))
	}
	if !MonthStart(at, bkk).Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, bkk)) || !DayStart(at, bkk).Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, bkk)) {
		t.Fatal("month or day start not in Bangkok")
	}
}
