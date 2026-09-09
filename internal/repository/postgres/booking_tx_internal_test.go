package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// Two transactions inserting conflicting rows at the same instant each see
// the other's in-progress tuple while checking the exclusion constraint;
// Postgres breaks the cycle by aborting one with 40P01. That transaction did
// not do anything wrong — it lost the race — so it must retry like a 23P01.
func TestMapInsertError_DeadlockIsALostRace(t *testing.T) {
	err := mapInsertError(&pgconn.PgError{Code: "40P01", Message: "deadlock detected"})
	if !errors.Is(err, service.ErrLostRace) {
		t.Fatalf("40P01 must be classified as a lost race, got %v", err)
	}
	if domain.CodeOf(err) != domain.CodeNoAvailableResource {
		t.Fatalf("lost race must carry a domain error, got %v", err)
	}
}

func TestMapInsertError_ExclusionConstraintsMapByName(t *testing.T) {
	cases := map[string]struct {
		code        domain.Code
		conflicting []domain.ResourceKind
	}{
		ConstraintBayOverlap:        {domain.CodeNoAvailableResource, []domain.ResourceKind{domain.ResourceBay}},
		ConstraintTechnicianOverlap: {domain.CodeNoAvailableResource, []domain.ResourceKind{domain.ResourceTechnician}},
		ConstraintVehicleOverlap:    {domain.CodeVehicleAlreadyBooked, nil},
	}
	for name, want := range cases {
		err := mapInsertError(&pgconn.PgError{Code: "23P01", ConstraintName: name})
		de, ok := domain.AsError(err)
		if !errors.Is(err, service.ErrLostRace) || !ok || de.Code != want.code || len(de.Conflicting) != len(want.conflicting) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if err := mapInsertError(&pgconn.PgError{Code: "23503"}); errors.Is(err, service.ErrLostRace) {
		t.Fatal("a foreign-key violation is not a lost race")
	}
}
