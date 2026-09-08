package domain

import (
	"testing"
	"time"
)

func TestBookingTime_AC02_EndIsStartPlusServiceDuration(t *testing.T) {
	start := local(monday, 9, 0)
	got, err := ValidateBookingTime(start, 60*time.Minute, early, hcm, weekHours)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Start.Equal(start) || !got.End.Equal(start.Add(time.Hour)) {
		t.Fatalf("interval = %v, want [09:00, 10:00)", got)
	}
}

func TestBookingTime_AC13_StartBeforeOpeningIsOutsideBusinessHours(t *testing.T) {
	_, err := ValidateBookingTime(local(monday, 7, 30), 30*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeOutsideBusinessHours)
}

func TestBookingTime_AC13_StartAtOrAfterClosingIsOutsideBusinessHours(t *testing.T) {
	// Starting exactly at closing time is outside hours, not "exceeds closing".
	_, err := ValidateBookingTime(local(monday, 17, 0), 30*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeOutsideBusinessHours)
	_, err = ValidateBookingTime(local(monday, 18, 0), 30*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeOutsideBusinessHours)
}

func TestBookingTime_AC14_EndingAfterClosingIsServiceExceedsClosingTime(t *testing.T) {
	_, err := ValidateBookingTime(local(monday, 16, 30), 60*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeServiceExceedsClosingTime)
}

func TestBookingTime_AC14_EndingExactlyAtClosingIsAccepted(t *testing.T) {
	if _, err := ValidateBookingTime(local(monday, 16, 0), 60*time.Minute, early, hcm, weekHours); err != nil {
		t.Fatalf("a job finishing exactly at closing is within hours (half-open): %v", err)
	}
	if _, err := ValidateBookingTime(local(monday, 8, 0), 30*time.Minute, early, hcm, weekHours); err != nil {
		t.Fatalf("a job starting exactly at opening is within hours: %v", err)
	}
}

func TestBookingTime_AC15_ClosedDayIsRejected(t *testing.T) {
	_, err := ValidateBookingTime(local(sunday, 9, 0), 30*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeOutsideBusinessHours)
}

func TestBookingTime_SaturdayUsesSaturdayHours(t *testing.T) {
	if _, err := ValidateBookingTime(local(saturday, 11, 0), 60*time.Minute, early, hcm, weekHours); err != nil {
		t.Fatalf("11:00–12:00 Saturday is within 08:00–12:00: %v", err)
	}
	_, err := ValidateBookingTime(local(saturday, 11, 30), 60*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeServiceExceedsClosingTime)
}

func TestBookingTime_AC16_StartInPastIsRejected(t *testing.T) {
	now := local(monday, 10, 0)
	_, err := ValidateBookingTime(local(monday, 9, 0), 30*time.Minute, now, hcm, weekHours)
	expectCode(t, err, CodeStartTimeInPast)
}

func TestBookingTime_AC16_PastTakesPrecedenceOverHours(t *testing.T) {
	// A past request outside hours is reported as in the past: the client
	// cannot fix it by picking a different hour on that day.
	now := local(monday, 10, 0)
	_, err := ValidateBookingTime(local(monday, 6, 0), 30*time.Minute, now, hcm, weekHours)
	expectCode(t, err, CodeStartTimeInPast)
}

func TestBookingTime_A9_InstantIsEvaluatedInDealershipTimezone(t *testing.T) {
	// 02:00 UTC on the Monday is 09:00 in Ho Chi Minh City — inside hours even
	// though the wall clock in UTC is not.
	startUTC := time.Date(2030, time.March, 4, 2, 0, 0, 0, time.UTC)
	if _, err := ValidateBookingTime(startUTC, 30*time.Minute, early, hcm, weekHours); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	// 12:00 UTC is 19:00 local — outside hours.
	_, err := ValidateBookingTime(startUTC.Add(10*time.Hour), 30*time.Minute, early, hcm, weekHours)
	expectCode(t, err, CodeOutsideBusinessHours)
}

func TestBusinessHours_WindowIsAbsoluteInDealershipTimezone(t *testing.T) {
	w, open := weekHours.Window(Date{2030, time.March, monday}, hcm)
	if !open {
		t.Fatal("Monday must be open")
	}
	if !w.Open.Equal(local(monday, 8, 0)) || !w.Close.Equal(local(monday, 17, 0)) {
		t.Fatalf("window = %v, want [08:00, 17:00) local", w)
	}
	if _, open := weekHours.Window(Date{2030, time.March, sunday}, hcm); open {
		t.Fatal("Sunday must be closed")
	}
}

func TestDate_ParseAndFormat(t *testing.T) {
	d, err := ParseDate("2030-03-04")
	if err != nil {
		t.Fatal(err)
	}
	if d != (Date{2030, time.March, 4}) || d.String() != "2030-03-04" {
		t.Fatalf("got %+v / %s", d, d)
	}
	for _, bad := range []string{"2030-3-4", "04/03/2030", "2030-13-01", "", "2030-02-30"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) must fail", bad)
		}
	}
	if got := DateOf(time.Date(2030, time.March, 4, 23, 30, 0, 0, time.UTC), hcm); got != (Date{2030, time.March, 5}) {
		t.Fatalf("DateOf must use the dealership's local date, got %v", got)
	}
}
