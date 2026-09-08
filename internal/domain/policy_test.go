package domain

import (
	"math/rand"
	"testing"
)

func TestQualification_AC08_TechnicianWithoutRequiredSkillIsFiltered(t *testing.T) {
	techs := schedule(nil, nil).Technicians
	got := QualifiedTechnicians(techs, skillAlignment)
	if len(got) != 1 || got[0].ID != techAn.ID {
		t.Fatalf("only An holds WHEEL_ALIGNMENT; got %v", got)
	}
	if len(QualifiedTechnicians(techs, "skill-does-not-exist")) != 0 {
		t.Fatal("unknown skill qualifies nobody")
	}
}

func TestQualification_AC09_BayOfWrongTypeIsFiltered(t *testing.T) {
	bays := schedule(nil, nil).Bays
	got := QualifiedBays(bays, BayTypeEV)
	if len(got) != 1 || got[0].ID != bayEV.ID {
		t.Fatalf("only the EV bay qualifies for EV work; got %v", got)
	}
	if n := len(QualifiedBays(bays, BayTypeGeneral)); n != 2 {
		t.Fatalf("two GENERAL bays expected, got %d", n)
	}
}

func TestPolicy_AC20_LessLoadedCandidateIsChosen(t *testing.T) {
	var p AssignmentPolicy = LeastLoadedPolicy{}
	got, ok := p.Choose([]Candidate{
		{ID: "tech-01", LoadMinutes: 240},
		{ID: "tech-02", LoadMinutes: 60},
		{ID: "tech-03", LoadMinutes: 120},
	})
	if !ok || got.ID != "tech-02" {
		t.Fatalf("chose %v, want tech-02 (fewest minutes)", got)
	}
}

func TestPolicy_AC21_EqualLoadTieBreaksByAscendingIDAndIsRepeatable(t *testing.T) {
	var p AssignmentPolicy = LeastLoadedPolicy{}
	base := []Candidate{
		{ID: "tech-03", LoadMinutes: 60},
		{ID: "tech-01", LoadMinutes: 60},
		{ID: "tech-02", LoadMinutes: 60},
		{ID: "tech-04", LoadMinutes: 90},
	}
	rng := rand.New(rand.NewSource(42))
	for run := 0; run < 200; run++ {
		in := append([]Candidate(nil), base...)
		rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
		got, ok := p.Choose(in)
		if !ok || got.ID != "tech-01" {
			t.Fatalf("run %d: chose %v, want tech-01 regardless of input order", run, got)
		}
	}
}

func TestPolicy_NoCandidatesReturnsFalse(t *testing.T) {
	if _, ok := (LeastLoadedPolicy{}).Choose(nil); ok {
		t.Fatal("empty candidate list must not yield a choice")
	}
}

func TestPolicy_DoesNotMutateInput(t *testing.T) {
	in := []Candidate{{ID: "b", LoadMinutes: 0}, {ID: "a", LoadMinutes: 0}}
	_, _ = (LeastLoadedPolicy{}).Choose(in)
	if in[0].ID != "b" {
		t.Fatal("policy must not reorder the caller's slice")
	}
}
