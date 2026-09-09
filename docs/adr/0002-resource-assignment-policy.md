# ADR-0002 — Resource assignment policy

**Status:** accepted · **Date:** 2026-09-09 · **Relates to:** BR-2, BR-3, BR-6, A-4, INV-4, INV-5, AC-08, AC-09, AC-20, AC-21

## Context

The client says *what* and *when*; the system must choose *who* and *where* (§3). Several
technicians may hold the required skill and several bays may have the required type. The choice
must be deterministic so it is testable and explainable to a workshop controller, while modelling
a real objective (spread load). Availability (FR-1) must make the same decision as booking (FR-2)
so the two endpoints never disagree on whether a slot is bookable.

## Decision

* **Qualification first.** `domain.QualifiedTechnicians` keeps technicians holding
  `ServiceType.RequiredSkillID`; `domain.QualifiedBays` keeps bays of `RequiredBayType`. Then only
  candidates whose confirmed intervals do not overlap the requested `[start, end)` remain. This is
  where INV-4 and INV-5 are enforced.
* **Policy behind an interface.**

  ```go
  type AssignmentPolicy interface { Choose(candidates []Candidate) (Candidate, bool) }
  ```

  `LeastLoadedPolicy` implements BR-6: fewest assigned minutes on the dealership-local date, ties
  broken by ascending identifier. It sorts a copy, so it is order-independent and never mutates
  its input. The same policy instance chooses the technician and the bay.
* **Load definition.** Sum of the durations of the resource's CONFIRMED appointments whose start
  falls within the local calendar day of the requested start (`domain.LoadMinutes`, selected by
  `Date.Bounds`). Appointments on other days do not count.
* **One function for both endpoints.** `domain.Assign` returns the pair or a precise error
  (`VEHICLE_ALREADY_BOOKED`, or `NO_AVAILABLE_RESOURCE` with `conflicting` listing `BAY`,
  `TECHNICIAN` or both). `domain.AvailableSlots` calls `Assign` for each candidate start time.
* **Precedence.** Resource availability is checked before the vehicle (INV-3). AC-18 states that N
  *identical* concurrent requests for the last qualifying bay/technician yield N−1
  `NO_AVAILABLE_RESOURCE`; identical requests share the vehicle, so the resource conflict must be
  reported first. `VEHICLE_ALREADY_BOOKED` is therefore returned only when a qualified bay and
  technician are free — which is also the case where it is the actionable message (AC-07).
* **Data loading.** The repository returns all technicians (with skills) and bays of the
  dealership plus the day's confirmed intervals; SQL performs no qualification or overlap logic.

## Alternatives considered

| Alternative | Why not |
|---|---|
| First qualified by id | Deterministic but piles work on one technician; fails the "real objective" test in A-4 |
| Random / round-robin | Not repeatable (AC-21); round-robin needs state |
| Least loaded over a rolling window or week | More "fair" but the spec says "on that date"; easy to swap behind the interface |
| Qualification via SQL joins | Fewer rows transferred, but moves a business rule into SQL contrary to §11 Maintainability; the domain function is unit-tested in isolation |
| Policy returns a ranked list to try in order on conflict | Would avoid the re-selection round-trip; rejected as premature — re-running the whole selection on fresh data is simpler and equally correct |
| Vehicle-first precedence (the original implementation) | More actionable when a car is double-booked *and* resources are gone, but contradicts the literal text of AC-18; reversed after review |

## Consequences

* Deterministic, so all replicas racing for the same slot select the same resources — which is
  why ADR-0001's bounded retry exists.
* Replacing the policy is a one-type change (`service.WithPolicy`).
* Load is recomputed per request from the day's rows; no counters to keep in sync.
* Loading a whole dealership's resources per request is trivially cheap for one dealership and
  would become the first optimisation target (push the skill/type filter into SQL) at scale.
