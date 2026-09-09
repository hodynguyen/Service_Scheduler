package domain

// TechnicianSchedule is a technician with the confirmed intervals they hold
// on the day being scheduled.
type TechnicianSchedule struct {
	Technician
	Booked []Interval
}

// BaySchedule is a bay with the confirmed intervals it holds on the day.
type BaySchedule struct {
	Bay
	Booked []Interval
}

// DaySchedule is everything the assignment needs to know about one
// dealership on one local day. It is loaded by the repository and contains
// only CONFIRMED appointments (BR-9).
type DaySchedule struct {
	Technicians   []TechnicianSchedule
	Bays          []BaySchedule
	VehicleBooked []Interval // confirmed appointments of the requesting vehicle
}

// Assignment is the outcome of a successful candidate selection.
type Assignment struct {
	Technician Technician
	Bay        Bay
}

// LoadMinutes sums the booked minutes (BR-6 "assigned minutes on that date").
func LoadMinutes(booked []Interval) int {
	var total int
	for _, b := range booked {
		total += int(b.Duration().Minutes())
	}
	return total
}

// Assign selects a technician and a bay for interval iv according to BR-2,
// BR-3 and the policy (BR-6). It returns, in this order of precedence:
//
//   - NO_AVAILABLE_RESOURCE listing which of BAY / TECHNICIAN has no free,
//     qualified candidate (AC-04..AC-06, AC-08, AC-09). Checked first so that
//     N identical requests for the last slot all fail with this code (AC-18).
//   - VEHICLE_ALREADY_BOOKED when resources are free but the vehicle overlaps
//     an active appointment (INV-3, AC-07)
//
// This is advisory: the database constraints are the correctness guarantee.
func Assign(s DaySchedule, st ServiceType, iv Interval, policy AssignmentPolicy) (Assignment, error) {
	techByID := map[string]Technician{}
	var techCandidates []Candidate
	for _, ts := range QualifiedTechnicians(s.Technicians, st.RequiredSkillID) {
		if OverlapsAny(iv, ts.Booked) {
			continue
		}
		techByID[ts.ID] = ts.Technician
		techCandidates = append(techCandidates, Candidate{ID: ts.ID, LoadMinutes: LoadMinutes(ts.Booked)})
	}

	bayByID := map[string]Bay{}
	var bayCandidates []Candidate
	for _, bs := range QualifiedBays(s.Bays, st.RequiredBayType) {
		if OverlapsAny(iv, bs.Booked) {
			continue
		}
		bayByID[bs.ID] = bs.Bay
		bayCandidates = append(bayCandidates, Candidate{ID: bs.ID, LoadMinutes: LoadMinutes(bs.Booked)})
	}

	conflicting := conflictingFor(len(bayCandidates) == 0, len(techCandidates) == 0)
	if len(conflicting) > 0 {
		return Assignment{}, &Error{
			Code:        CodeNoAvailableResource,
			Message:     "no qualifying free resource for " + iv.String(),
			Conflicting: conflicting,
		}
	}
	if OverlapsAny(iv, s.VehicleBooked) {
		return Assignment{}, NewError(CodeVehicleAlreadyBooked, "vehicle already has an active appointment overlapping %s", iv)
	}

	tech, okTech := policy.Choose(techCandidates)
	bay, okBay := policy.Choose(bayCandidates)
	if !okTech || !okBay {
		// A custom policy declined to choose; report it as unavailability
		// rather than inserting empty identifiers.
		return Assignment{}, &Error{Code: CodeNoAvailableResource, Message: "assignment policy selected no resource for " + iv.String(),
			Conflicting: conflictingFor(!okBay, !okTech)}
	}
	return Assignment{Technician: techByID[tech.ID], Bay: bayByID[bay.ID]}, nil
}

// conflictingFor builds the §10 conflicting list in its fixed order.
func conflictingFor(noBay, noTechnician bool) []ResourceKind {
	var out []ResourceKind
	if noBay {
		out = append(out, ResourceBay)
	}
	if noTechnician {
		out = append(out, ResourceTechnician)
	}
	return out
}
