package domain

import (
	"testing"
	"time"
)

func slotsAt(slots []time.Time) []string {
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = s.In(hcm).Format("15:04")
	}
	return out
}

func equalSlots(t *testing.T, got []time.Time, want ...string) {
	t.Helper()
	g := slotsAt(got)
	if len(g) != len(want) {
		t.Fatalf("slots = %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("slots = %v, want %v", g, want)
		}
	}
}

func mondayWindow(t *testing.T) DayWindow {
	t.Helper()
	w, ok := weekHours.Window(Date{2030, time.March, monday}, hcm)
	if !ok {
		t.Fatal("Monday must be open")
	}
	return w
}

func TestAvailability_GeneratesSlotsAtGranularityWithinHours(t *testing.T) {
	// 08:00–17:00, 60-minute job, 30-minute steps: 08:00 … 16:00 (last slot ends at 17:00).
	got := AvailableSlots(mondayWindow(t), svcAlignment, schedule(nil, nil), early, 30*time.Minute, policy)
	if len(got) != 17 {
		t.Fatalf("got %d slots %v, want 17 (08:00..16:00 every 30 min)", len(got), slotsAt(got))
	}
	if s := slotsAt(got); s[0] != "08:00" || s[len(s)-1] != "16:00" {
		t.Fatalf("slots = %v", s)
	}
}

func TestAvailability_AC22_ExcludesSlotsThatWouldFailAnInvariant(t *testing.T) {
	// Alignment: only An and the alignment rig qualify. An is booked 10:00–11:00.
	booked := []Interval{iv(monday, 10, 0, time.Hour)}
	s := schedule(map[string][]Interval{techAn.ID: booked}, nil)
	got := AvailableSlots(mondayWindow(t), svcAlignment, s, early, 30*time.Minute, policy)
	for _, slot := range slotsAt(got) {
		if slot == "09:30" || slot == "10:00" || slot == "10:30" {
			t.Fatalf("slot %s overlaps An's 10:00–11:00 booking; slots = %v", slot, slotsAt(got))
		}
	}
	// 09:00 (ends 10:00) and 11:00 (starts at 11:00) survive: half-open.
	has := map[string]bool{}
	for _, s := range slotsAt(got) {
		has[s] = true
	}
	if !has["09:00"] || !has["11:00"] {
		t.Fatalf("09:00 and 11:00 must remain available, got %v", slotsAt(got))
	}
}

func TestAvailability_AC22_ExcludesSlotsWhereVehicleIsBusy(t *testing.T) {
	s := schedule(nil, nil)
	s.VehicleBooked = []Interval{iv(monday, 9, 0, 30*time.Minute)}
	got := AvailableSlots(mondayWindow(t), svcOil, s, early, 30*time.Minute, policy)
	for _, slot := range slotsAt(got) {
		if slot == "09:00" {
			t.Fatalf("vehicle is busy at 09:00 (INV-3); slots = %v", slotsAt(got))
		}
	}
}

func TestAvailability_AC23_ExcludesSlotsRunningPastClosing(t *testing.T) {
	// Saturday 08:00–12:00, 90-minute EV job: last valid start is 10:30.
	w, _ := weekHours.Window(Date{2030, time.March, saturday}, hcm)
	got := AvailableSlots(w, svcEV, schedule(nil, nil), early, 30*time.Minute, policy)
	equalSlots(t, got, "08:00", "08:30", "09:00", "09:30", "10:00", "10:30")
}

func TestAvailability_AC24_FullyBookedDayReturnsEmptySlice(t *testing.T) {
	allDay := []Interval{iv(saturday, 8, 0, 4*time.Hour)}
	w, _ := weekHours.Window(Date{2030, time.March, saturday}, hcm)
	s := schedule(map[string][]Interval{techChi.ID: allDay}, map[string][]Interval{bayEV.ID: allDay})
	got := AvailableSlots(w, svcEV, s, early, 30*time.Minute, policy)
	if got == nil || len(got) != 0 {
		t.Fatalf("want a non-nil empty slice (serialises as []), got %#v", got)
	}
}

func TestAvailability_BR5_ExcludesSlotsInThePast(t *testing.T) {
	now := local(monday, 10, 15)
	got := AvailableSlots(mondayWindow(t), svcOil, schedule(nil, nil), now, 30*time.Minute, policy)
	if s := slotsAt(got); s[0] != "10:30" {
		t.Fatalf("first slot must be the first one not in the past, got %v", s)
	}
}

func TestAvailability_SlotsAreReturnedInDealershipLocation(t *testing.T) {
	got := AvailableSlots(mondayWindow(t), svcOil, schedule(nil, nil), early, 30*time.Minute, policy)
	if len(got) == 0 || got[0].Location().String() != hcm.String() {
		t.Fatalf("slots must carry the dealership location for offset-aware formatting")
	}
}
