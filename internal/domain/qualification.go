package domain

// QualifiedTechnicians keeps the technicians holding skillID (BR-2, INV-4).
func QualifiedTechnicians(techs []Technician, skillID string) []Technician {
	out := make([]Technician, 0, len(techs))
	for _, t := range techs {
		if t.HasSkill(skillID) {
			out = append(out, t)
		}
	}
	return out
}

// QualifiedBays keeps the bays of the required type (BR-3, INV-5).
func QualifiedBays(bays []Bay, bayType BayType) []Bay {
	out := make([]Bay, 0, len(bays))
	for _, b := range bays {
		if b.Type == bayType {
			out = append(out, b)
		}
	}
	return out
}
