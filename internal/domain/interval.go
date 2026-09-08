package domain

import (
	"fmt"
	"time"
)

// Interval is a half-open time range [Start, End). Two intervals that share
// a boundary do not overlap (A-5, AC-10, AC-11).
type Interval struct {
	Start time.Time
	End   time.Time
}

// NewInterval builds [start, start+d).
func NewInterval(start time.Time, d time.Duration) Interval {
	return Interval{Start: start, End: start.Add(d)}
}

// Duration is End − Start.
func (i Interval) Duration() time.Duration { return i.End.Sub(i.Start) }

// Overlaps reports whether the two half-open ranges share any instant.
// Comparison is by instant, so locations do not matter.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start.Before(o.End) && o.Start.Before(i.End)
}

// OverlapsAny reports whether i overlaps at least one of others.
func OverlapsAny(i Interval, others []Interval) bool {
	for _, o := range others {
		if i.Overlaps(o) {
			return true
		}
	}
	return false
}

func (i Interval) String() string {
	return fmt.Sprintf("[%s, %s)", i.Start.Format(time.RFC3339), i.End.Format(time.RFC3339))
}
