# ADR-0001 — Double-booking prevention with PostgreSQL exclusion constraints

**Status:** accepted · **Date:** 2026-09-09 · **Relates to:** INV-1, INV-2, INV-3, BR-9, AC-10..AC-12, AC-17, AC-18

## Context

A bay, a technician and a vehicle may each be in at most one *active* appointment at a time
(INV-1..3). The system must hold this under concurrent requests from a horizontally scaled,
stateless service (§11). Availability is advisory, so a "check then insert" in the application is
a read-modify-write race: two requests can both see a free slot and both insert. Cancelled
appointments must release their resources immediately (BR-9), and adjacent intervals must not
conflict (half-open ranges, A-5).

## Decision

Declare the invariants in the schema and let the database adjudicate:

```sql
CREATE EXTENSION btree_gist;
CONSTRAINT appointment_no_bay_overlap
    EXCLUDE USING gist (bay_id WITH =, tstzrange(start_time, end_time, '[)') WITH &&)
    WHERE (status = 'CONFIRMED')
-- identical constraints for technician_id and vehicle_id
```

* `tstzrange(..., '[)')` gives half-open semantics: `[09:00,10:00)` and `[10:00,11:00)` do not
  overlap.
* The partial predicate `WHERE status = 'CONFIRMED'` is what makes cancellation release resources
  without deleting history.
* The application still runs a candidate selection (`domain.Assign`) so that rejections carry a
  precise code and `conflicting` list, but it is *not* the guarantee. When the insert fails with
  SQLSTATE `23P01`, the repository maps the constraint name to the resource, the service rolls back
  to a savepoint, reloads the day's schedule (READ COMMITTED sees the competitor's row) and either
  picks a spare resource or returns the domain's precise rejection. Attempts are bounded (3).
* INV-7 is enforced the same way in spirit: composite foreign keys `(resource_id, dealership_id)`.
* **Selection is serialised per dealership-day** with `pg_advisory_xact_lock(hashtext(dealership),
  hashtext('day:'+date))`, taken inside the booking transaction after the idempotency lock (fixed
  order key → day, so the two locks cannot cycle). This is *not* part of the guarantee: it exists
  because N simultaneous conflicting inserts each see the others' in-progress tuples during the
  exclusion check and form wait cycles that Postgres breaks one victim per `deadlock_timeout`
  (1 s), and because the deterministic policy makes every loser re-pick the same resource while
  the winner is still uncommitted. Observed on a 2-vCPU CI runner (84 s for four bookings). With
  the lock, same-day bookings select one after another on committed data: no deadlocks, no
  spurious rejections, the constraint still catches any writer that bypasses the lock.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Application check + insert, no constraint | Race; violates §8 note explicitly |
| `SELECT … FOR UPDATE` on bay/technician rows | Serialises all bookings for a resource for the whole transaction, including non-overlapping ones; still needs correct overlap logic in code; does not cover the vehicle without another lock |
| Advisory lock as the *guarantee* (per resource or per day, without constraints) | Correctness would depend on every writer honouring the lock protocol; a per-day advisory lock is used here only to order selection, with the constraints still enforcing the invariant |
| `SERIALIZABLE` isolation | Correct but yields serialization failures for unrelated rows sharing pages; retry logic is more complex than reacting to a named constraint |
| Unique index on discretised slots (resource, slot_start) | Only works for fixed-length jobs aligned to the grid; service types have different durations |
| Trigger with an overlap query | Reimplements what the exclusion constraint does declaratively, with the same locking needs and more surface for bugs |

## Consequences

* Correctness no longer depends on application memory or on there being a single replica.
* Same-dealership, same-day bookings are serialised by the advisory lock (a few milliseconds of
  selection + insert each); different days and dealerships proceed in parallel. The constraint
  remains the only thing that makes overlapping rows impossible.
* Because BR-6 is deterministic, concurrent requests tend to pick the same first candidate; the
  bounded retry exists so a lost race does not become a spurious rejection while other resources
  are free (tested).
* Every insert path must go through the same table; there is no way to "forget" the check.
* `btree_gist` must be available (it is in the standard `postgres:16` image).
* INV-4/INV-5 (skill and bay-type match) are covered since migration 0002 by composite foreign
  keys: `appointment.required_skill_id / required_bay_type` are pinned to the service type and
  the technician/bay must match them (`technician_skill(technician_id, skill_id)`,
  `service_bay(id, bay_type)`). Trade-off: a service type's requirement, a technician's
  certification or a bay's type cannot be changed while appointments reference it (RESTRICT);
  historical rows need a data migration first.
