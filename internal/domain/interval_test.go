package domain

import (
	"testing"
	"time"
)

func TestInterval_AC10_EndingExactlyWhenAnotherBeginsDoesNotOverlap(t *testing.T) {
	a := iv(monday, 9, 0, time.Hour)  // [09:00, 10:00)
	b := iv(monday, 10, 0, time.Hour) // [10:00, 11:00)
	if a.Overlaps(b) || b.Overlaps(a) {
		t.Fatalf("half-open intervals sharing a boundary must not overlap: %v vs %v", a, b)
	}
}

func TestInterval_AC11_StartingExactlyWhenAnotherEndsDoesNotOverlap(t *testing.T) {
	existing := iv(monday, 8, 0, time.Hour) // [08:00, 09:00)
	candidate := iv(monday, 9, 0, 30*time.Minute)
	if OverlapsAny(candidate, []Interval{existing}) {
		t.Fatal("a booking starting when another ends must be accepted")
	}
}

func TestInterval_AC12_OneMinuteOverlapIsDetected(t *testing.T) {
	a := iv(monday, 9, 0, time.Hour)       // [09:00, 10:00)
	b := iv(monday, 9, 59, 30*time.Minute) // [09:59, 10:29)
	c := iv(monday, 8, 1, 60*time.Minute)  // [08:01, 09:01)
	if !a.Overlaps(b) || !b.Overlaps(a) {
		t.Fatalf("%v and %v overlap by one minute at the end", a, b)
	}
	if !a.Overlaps(c) || !c.Overlaps(a) {
		t.Fatalf("%v and %v overlap by one minute at the start", a, c)
	}
}

func TestInterval_ContainmentAndIdentityOverlap(t *testing.T) {
	outer := iv(monday, 9, 0, 3*time.Hour)
	inner := iv(monday, 10, 0, 30*time.Minute)
	if !outer.Overlaps(inner) || !inner.Overlaps(outer) {
		t.Fatal("containment is an overlap")
	}
	if !outer.Overlaps(outer) {
		t.Fatal("an interval overlaps itself")
	}
}

func TestInterval_OverlapIsIndependentOfLocation(t *testing.T) {
	a := iv(monday, 9, 0, time.Hour)
	b := Interval{Start: a.Start.UTC(), End: a.End.UTC()}
	if !a.Overlaps(b) {
		t.Fatal("same instants in different locations must overlap")
	}
}

func TestInterval_AC02_DurationIsEndMinusStart(t *testing.T) {
	i := NewInterval(local(monday, 9, 0), 90*time.Minute)
	if got := i.End.Sub(i.Start); got != 90*time.Minute {
		t.Fatalf("end - start = %v, want 90m", got)
	}
	if i.Duration() != 90*time.Minute {
		t.Fatalf("Duration() = %v", i.Duration())
	}
}
