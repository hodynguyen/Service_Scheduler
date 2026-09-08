package domain

import "sort"

// Candidate is a qualified, free resource with its load for the day.
type Candidate struct {
	ID          string
	LoadMinutes int
}

// AssignmentPolicy chooses one resource among qualified, free candidates.
// Implementations must be deterministic: the same candidate set must yield
// the same choice regardless of input order (AC-21).
type AssignmentPolicy interface {
	Choose(candidates []Candidate) (Candidate, bool)
}

// LeastLoadedPolicy implements BR-6 / A-4: fewest assigned minutes on the
// day wins; ties are broken by ascending identifier.
type LeastLoadedPolicy struct{}

// Choose returns the least-loaded candidate, or false when there are none.
func (LeastLoadedPolicy) Choose(candidates []Candidate) (Candidate, bool) {
	if len(candidates) == 0 {
		return Candidate{}, false
	}
	sorted := append([]Candidate(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].LoadMinutes != sorted[j].LoadMinutes {
			return sorted[i].LoadMinutes < sorted[j].LoadMinutes
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted[0], true
}
