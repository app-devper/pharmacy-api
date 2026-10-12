// Package calendar is the pharmacy's calendar: business days and months in
// the shop's timezone, query periods given as inclusive dates, and the
// day-keyed document numbers (INV, RET, SC, IMP).
package calendar

import "time"

// DayLayout is how a business day is written (YYYY-MM-DD).
const DayLayout = "2006-01-02"

// DayStart is midnight of t's calendar day in tz.
func DayStart(t time.Time, tz *time.Location) time.Time {
	t = t.In(tz)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz)
}

// MonthStart is midnight of the first day of t's month in tz.
func MonthStart(t time.Time, tz *time.Location) time.Time {
	t = t.In(tz)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, tz)
}

// Day is t's business day in tz, as written (YYYY-MM-DD).
func Day(t time.Time, tz *time.Location) string { return t.In(tz).Format(DayLayout) }

// Dates is the end-exclusive period [start, end) covering the inclusive
// dates from..to (YYYY-MM-DD) in tz. A missing or malformed date leaves its
// bound zero, which callers treat as open or replace with their default.
// The end is the next midnight, so a day that is not 24 hours long (a DST
// change) is still covered exactly.
func Dates(tz *time.Location, from, to string) (start, end time.Time) {
	if t, err := time.ParseInLocation(DayLayout, from, tz); err == nil {
		start = t
	}
	if t, err := time.ParseInLocation(DayLayout, to, tz); err == nil {
		end = t.AddDate(0, 0, 1)
	}
	return start, end
}
