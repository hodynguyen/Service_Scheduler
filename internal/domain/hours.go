package domain

import (
	"fmt"
	"time"
)

// OpeningHours are the opening and closing wall-clock times of one weekday,
// as minutes after local midnight. Half-open: the shop is open on
// [OpensAt, ClosesAt).
type OpeningHours struct {
	OpensAt  int
	ClosesAt int
}

// BusinessHours maps a weekday to its opening hours. A missing weekday means
// the dealership is closed that day.
type BusinessHours map[time.Weekday]OpeningHours

// Date is a civil date in the dealership's calendar.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate parses YYYY-MM-DD strictly (rejects impossible dates).
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// DateOf returns the civil date of an instant in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	l := t.In(loc)
	return Date{Year: l.Year(), Month: l.Month(), Day: l.Day()}
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Weekday returns the weekday of the civil date.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Weekday()
}

// DayWindow is the absolute [Open, Close) interval of one business day.
type DayWindow struct {
	Open  time.Time
	Close time.Time
}

// Window resolves the opening hours of a date into absolute instants in loc.
// ok is false when the dealership is closed that day. Wall-clock times are
// built with time.Date so daylight-saving transitions keep the local clock.
func (h BusinessHours) Window(d Date, loc *time.Location) (w DayWindow, ok bool) {
	oh, open := h[d.Weekday()]
	if !open {
		return DayWindow{}, false
	}
	w.Open = time.Date(d.Year, d.Month, d.Day, 0, oh.OpensAt, 0, 0, loc)
	w.Close = time.Date(d.Year, d.Month, d.Day, 0, oh.ClosesAt, 0, 0, loc)
	return w, true
}

// ValidateBookingTime applies BR-5 then BR-4 and returns the appointment
// interval [start, start+duration).
//
//   - start < now                      -> START_TIME_IN_PAST
//   - closed day, or start outside     -> OUTSIDE_BUSINESS_HOURS
//     [open, close)
//   - start inside but end > close     -> SERVICE_EXCEEDS_CLOSING_TIME
func ValidateBookingTime(start time.Time, duration time.Duration, now time.Time, loc *time.Location, hours BusinessHours) (Interval, error) {
	if start.Before(now) {
		return Interval{}, NewError(CodeStartTimeInPast, "start time %s is in the past", start.In(loc).Format(time.RFC3339))
	}
	date := DateOf(start, loc)
	window, open := hours.Window(date, loc)
	if !open {
		return Interval{}, NewError(CodeOutsideBusinessHours, "dealership is closed on %s (%s)", date.Weekday(), date)
	}
	if start.Before(window.Open) || !start.Before(window.Close) {
		return Interval{}, NewError(CodeOutsideBusinessHours, "start time %s is outside opening hours %s–%s",
			start.In(loc).Format("15:04"), window.Open.Format("15:04"), window.Close.Format("15:04"))
	}
	iv := NewInterval(start, duration)
	if iv.End.After(window.Close) {
		return Interval{}, NewError(CodeServiceExceedsClosingTime, "service would finish at %s, after closing time %s",
			iv.End.In(loc).Format("15:04"), window.Close.Format("15:04"))
	}
	return iv, nil
}

// Bounds returns the absolute [midnight, next midnight) interval of the civil
// date in loc. It is the range used to decide which appointments belong to
// "that date" for BR-6 load counting.
func (d Date) Bounds(loc *time.Location) Interval {
	start := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
	return Interval{Start: start, End: time.Date(d.Year, d.Month, d.Day+1, 0, 0, 0, 0, loc)}
}
