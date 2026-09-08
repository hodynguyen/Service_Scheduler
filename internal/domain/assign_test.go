package domain

import (
	"testing"
	"time"
)

var policy = LeastLoadedPolicy{}

func TestAssign_AC01_AvailableSlotAssignsTechnicianAndBay(t *testing.T) {
	got, err := Assign(schedule(nil, nil), svcAlignment, iv(monday, 9, 0, time.Hour), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got.Technician.ID != techAn.ID || got.Bay.ID != bayAlignment.ID {
		t.Fatalf("assigned %s/%s, want An on the alignment rig", got.Technician.ID, got.Bay.ID)
	}
}

func TestAssign_AC04_AllQualifyingBaysBusyReportsBayConflictOnly(t *testing.T) {
	// Oil change needs a GENERAL bay. Bays 1 and 2 are busy; four technicians
	// hold GENERAL_SERVICE and only two of them are busy.
	busy := []Interval{iv(monday, 9, 0, 30*time.Minute)}
	s := schedule(
		map[string][]Interval{techAn.ID: busy, techBinh.ID: busy},
		map[string][]Interval{bay1.ID: busy, bay2.ID: busy},
	)
	_, err := Assign(s, svcOil, iv(monday, 9, 0, 30*time.Minute), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceBay)
}

func TestAssign_AC05_AllQualifyingTechniciansBusyReportsTechnicianConflictOnly(t *testing.T) {
	// Transmission: only Binh qualifies; GENERAL bay 2 remains free.
	busy := []Interval{iv(monday, 9, 0, 3*time.Hour)}
	s := schedule(
		map[string][]Interval{techBinh.ID: busy},
		map[string][]Interval{bay1.ID: busy},
	)
	_, err := Assign(s, svcTransmission, iv(monday, 9, 0, 3*time.Hour), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceTechnician)
}

func TestAssign_AC06_BothBusyReportsBoth(t *testing.T) {
	busy := []Interval{iv(monday, 9, 0, 90*time.Minute)}
	s := schedule(
		map[string][]Interval{techChi.ID: busy},
		map[string][]Interval{bayEV.ID: busy},
	)
	_, err := Assign(s, svcEV, iv(monday, 9, 0, 90*time.Minute), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceBay, ResourceTechnician)
}

func TestAssign_AC07_VehicleWithOverlappingActiveAppointmentIsRejected(t *testing.T) {
	s := schedule(nil, nil)
	s.VehicleBooked = []Interval{iv(monday, 9, 0, 30*time.Minute)}
	_, err := Assign(s, svcAlignment, iv(monday, 9, 15, time.Hour), policy)
	_ = expectCode(t, err, CodeVehicleAlreadyBooked)
}

func TestAssign_AC07_VehicleCheckWinsOverResourceConflict(t *testing.T) {
	busy := []Interval{iv(monday, 9, 0, 90*time.Minute)}
	s := schedule(map[string][]Interval{techChi.ID: busy}, map[string][]Interval{bayEV.ID: busy})
	s.VehicleBooked = busy
	_, err := Assign(s, svcEV, iv(monday, 9, 0, 90*time.Minute), policy)
	_ = expectCode(t, err, CodeVehicleAlreadyBooked)
}

func TestAssign_AC08_FreeButUnqualifiedTechnicianIsNotAssigned(t *testing.T) {
	// EV work: Chi (qualified) is busy; Dung is free but unqualified.
	busy := []Interval{iv(monday, 9, 0, 90*time.Minute)}
	s := schedule(map[string][]Interval{techChi.ID: busy}, nil)
	_, err := Assign(s, svcEV, iv(monday, 9, 0, 90*time.Minute), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceTechnician)
}

func TestAssign_AC08_NoTechnicianHoldsSkillIsRejected(t *testing.T) {
	svc := ServiceType{ID: "svc-x", Name: "Nobody can do this", Duration: time.Hour, RequiredSkillID: "skill-missing", RequiredBayType: BayTypeGeneral}
	_, err := Assign(schedule(nil, nil), svc, iv(monday, 9, 0, time.Hour), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceTechnician)
}

func TestAssign_AC09_FreeBayOfWrongTypeIsNotAssigned(t *testing.T) {
	// EV bay busy; the alignment rig and both GENERAL bays are free.
	busy := []Interval{iv(monday, 9, 0, 90*time.Minute)}
	s := schedule(nil, map[string][]Interval{bayEV.ID: busy})
	_, err := Assign(s, svcEV, iv(monday, 9, 0, 90*time.Minute), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceBay)
}

func TestAssign_AC10_EndingWhenAnotherBeginsIsAccepted(t *testing.T) {
	existing := []Interval{iv(monday, 10, 0, time.Hour)}
	s := schedule(map[string][]Interval{techAn.ID: existing}, map[string][]Interval{bayAlignment.ID: existing})
	got, err := Assign(s, svcAlignment, iv(monday, 9, 0, time.Hour), policy)
	if err != nil {
		t.Fatalf("[09:00,10:00) must not conflict with [10:00,11:00): %v", err)
	}
	if got.Technician.ID != techAn.ID || got.Bay.ID != bayAlignment.ID {
		t.Fatalf("got %s/%s", got.Technician.ID, got.Bay.ID)
	}
}

func TestAssign_AC11_StartingWhenAnotherEndsIsAccepted(t *testing.T) {
	existing := []Interval{iv(monday, 9, 0, time.Hour)}
	s := schedule(map[string][]Interval{techAn.ID: existing}, map[string][]Interval{bayAlignment.ID: existing})
	if _, err := Assign(s, svcAlignment, iv(monday, 10, 0, time.Hour), policy); err != nil {
		t.Fatalf("[10:00,11:00) must not conflict with [09:00,10:00): %v", err)
	}
}

func TestBooking_AC12_OneMinuteOverlapIsRejected(t *testing.T) {
	existing := []Interval{iv(monday, 9, 0, time.Hour)} // [09:00, 10:00)
	s := schedule(map[string][]Interval{techAn.ID: existing}, map[string][]Interval{bayAlignment.ID: existing})
	_, err := Assign(s, svcAlignment, iv(monday, 9, 59, time.Hour), policy)
	de := expectCode(t, err, CodeNoAvailableResource)
	expectConflicting(t, de, ResourceBay, ResourceTechnician)
}

func TestAssign_AC17_CancelledAppointmentsDoNotOccupyResources(t *testing.T) {
	appts := []Appointment{
		{ID: "a1", Status: StatusCancelled, Interval: iv(monday, 9, 0, time.Hour)},
		{ID: "a2", Status: StatusConfirmed, Interval: iv(monday, 13, 0, time.Hour)},
	}
	booked := ActiveIntervals(appts)
	if len(booked) != 1 || !booked[0].Start.Equal(local(monday, 13, 0)) {
		t.Fatalf("only CONFIRMED appointments reserve resources, got %v", booked)
	}
	s := schedule(map[string][]Interval{techAn.ID: booked}, map[string][]Interval{bayAlignment.ID: booked})
	if _, err := Assign(s, svcAlignment, iv(monday, 9, 0, time.Hour), policy); err != nil {
		t.Fatalf("the cancelled 09:00 slot must be bookable again: %v", err)
	}
}

func TestAssign_AC20_LessLoadedTechnicianIsAssigned(t *testing.T) {
	// Oil change: all four technicians qualify. An has 4 h booked, Binh 1 h, Chi 2 h, Dung 3 h.
	s := schedule(map[string][]Interval{
		techAn.ID:   {iv(monday, 13, 0, 4*time.Hour)},
		techBinh.ID: {iv(monday, 13, 0, 1*time.Hour)},
		techChi.ID:  {iv(monday, 13, 0, 2*time.Hour)},
		techDung.ID: {iv(monday, 13, 0, 3*time.Hour)},
	}, nil)
	got, err := Assign(s, svcOil, iv(monday, 9, 0, 30*time.Minute), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got.Technician.ID != techBinh.ID {
		t.Fatalf("assigned %s, want Binh (least loaded)", got.Technician.ID)
	}
}

func TestAssign_AC20_LessLoadedBayIsAssigned(t *testing.T) {
	s := schedule(nil, map[string][]Interval{
		bay1.ID: {iv(monday, 13, 0, 3*time.Hour)},
		bay2.ID: {iv(monday, 13, 0, 30*time.Minute)},
	})
	got, err := Assign(s, svcOil, iv(monday, 9, 0, 30*time.Minute), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bay.ID != bay2.ID {
		t.Fatalf("assigned %s, want Bay 2 (least loaded)", got.Bay.ID)
	}
}

func TestAssign_AC20_LoadCountsOnlyThatDaysBookings(t *testing.T) {
	// Busy-but-free-at-the-slot technicians still carry their load.
	if got := LoadMinutes([]Interval{iv(monday, 13, 0, 90*time.Minute), iv(monday, 15, 0, 30*time.Minute)}); got != 120 {
		t.Fatalf("LoadMinutes = %d, want 120", got)
	}
}

func TestAssign_AC21_EqualLoadAssignsAscendingIdentifierRepeatably(t *testing.T) {
	for run := 0; run < 50; run++ {
		got, err := Assign(schedule(nil, nil), svcOil, iv(monday, 9, 0, 30*time.Minute), policy)
		if err != nil {
			t.Fatal(err)
		}
		if got.Technician.ID != techAn.ID || got.Bay.ID != bay1.ID {
			t.Fatalf("run %d: got %s/%s, want tech-01/bay-01", run, got.Technician.ID, got.Bay.ID)
		}
	}
}
