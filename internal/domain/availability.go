package domain

import "time"

// DefaultSlotGranularity is the step between candidate start times (FR-1).
// The specification fixes neither the value nor where it lives; 30 minutes
// matches the example response in §10.1.
const DefaultSlotGranularity = 30 * time.Minute

// AvailableSlots returns every start time in window, stepping by
// granularity, at which st could be booked given the day's schedule:
//
//   - the whole job fits before closing (AC-23)
//   - the slot is not in the past (BR-5)
//   - Assign succeeds, i.e. a qualified free technician and bay exist and
//     the vehicle (if known) is free (AC-22)
//
// The result is never nil so it serialises as [] (AC-24). Slots carry the
// window's location so they format with the dealership's offset.
func AvailableSlots(window DayWindow, st ServiceType, s DaySchedule, now time.Time, granularity time.Duration, policy AssignmentPolicy) []time.Time {
	slots := make([]time.Time, 0)
	if granularity <= 0 || st.Duration <= 0 {
		return slots
	}
	loc := window.Open.Location()
	for start := window.Open; !start.Add(st.Duration).After(window.Close); start = start.Add(granularity) {
		if start.Before(now) {
			continue
		}
		if _, err := Assign(s, st, NewInterval(start, st.Duration), policy); err != nil {
			continue
		}
		slots = append(slots, start.In(loc))
	}
	return slots
}
