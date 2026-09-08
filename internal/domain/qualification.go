package domain

// QualifiedTechnicians keeps the technicians holding skillID (BR-2, INV-4).
// It operates on day schedules so Assign uses it directly.
func QualifiedTechnicians(techs []TechnicianSchedule, skillID string) []TechnicianSchedule {
	out := make([]TechnicianSchedule, 0, len(techs))
	for _, t := range techs {
		if t.HasSkill(skillID) {
			out = append(out, t)
		}
	}
	return out
}

// QualifiedBays keeps the bays of the required type (BR-3, INV-5).
func QualifiedBays(bays []BaySchedule, bayType BayType) []BaySchedule {
	out := make([]BaySchedule, 0, len(bays))
	for _, b := range bays {
		if b.Type == bayType {
			out = append(out, b)
		}
	}
	return out
}
