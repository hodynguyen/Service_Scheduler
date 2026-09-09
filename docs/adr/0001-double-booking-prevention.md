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
  picks a spare resource or returns the domain's precise rejection. Attempts are bounded; see
  "Retry budget" below.
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

### Retry budget

The assignment policy (ADR-0002) is deterministic: given the same day schedule it returns the same
technician and bay. That makes the contention pattern predictable. When N requests race for one
slot, all of them select the same first candidate pair; one insert commits and every other request
loses on the constraint, re-reads the schedule, and — because the schedule now differs only by the
winner's row — all of them select the same *next* candidate pair. Each round therefore retires
exactly one competitor and consumes exactly one qualified technician and one qualified bay. After
`min(qualified technicians, qualified bays)` rounds either every request has been placed or no
candidate pair remains, and `domain.Assign` then returns the precise rejection. The budget is
`max(3, min(qualified technicians, qualified bays) + 1)`, computed from the first schedule load; the
`+1` is the round in which selection fails cleanly, the floor of 3 keeps a small margin for the
per-day lock being bypassed by a future writer. A budget that runs out while selection would still
succeed is reported as a transient rejection and deliberately **not** written to the idempotency
store, so a client retry with the same key re-evaluates instead of replaying a rejection caused by
contention rather than by the request.

With the per-day advisory lock in place the retry rarely runs at all: same-day competitors select
one after another on committed data. The budget remains as defence in depth.

### INV-4 / INV-5 foreign keys and RESTRICT

Migration `0002` adds `appointment.required_skill_id` and `required_bay_type`, pinned to the
service type by composite FKs `(service_type_id, required_*) → service_type(id, required_*)`, and
checked against the resources by `(technician_id, required_skill_id) → technician_skill` and
`(bay_id, required_bay_type) → service_bay(id, bay_type)`. All four are plain foreign keys with
the default `ON UPDATE/DELETE RESTRICT`.

Operational cost, stated plainly: decertifying a technician (deleting a `technician_skill` row),
retyping a bay, or changing a service type's requirement is blocked while **any** appointment —
CONFIRMED or CANCELLED — references the old value. In a real workshop, revoking an EV high-voltage
certification is safety-critical and cannot wait for the schedule to drain. This is a known
limitation of the current schema, accepted for this scope because the alternative could not be
expressed as a declarative constraint. Migration path, when it is needed:

* **Status-scoped check.** PostgreSQL foreign keys cannot be partial, so a scope of "CONFIRMED
  rows only" needs a trigger on `appointment` (and on `technician_skill` / `service_bay` deletes and
  updates) that checks the join only for active appointments. Cancelled history then no longer
  pins certifications.
* **Move the check to the service layer with an audit trail.** Drop the two resource FKs, keep the
  service-type pins, and record `(technician_id, skill_id, valid_from, valid_to)` so a revocation
  is an event: future bookings are filtered by validity, existing CONFIRMED appointments are
  surfaced for reassignment rather than blocked at the database.

Either path keeps INV-1..INV-3 exactly as they are.

## Schema subtleties

### The `start_time < end_time` CHECK is load-bearing

In PostgreSQL `tstzrange(t, t, '[)')` is the empty range, and an empty range overlaps nothing — not
even itself. A zero-length appointment (`start_time = end_time`) would therefore pass all three
exclusion constraints and could be inserted on top of any other booking. The constraint
`appointment_start_before_end CHECK (start_time < end_time)` is what makes the exclusion
constraints total. It is not input hygiene; removing it would silently reopen double booking for a
degenerate row. `service_type.duration_minutes > 0` guards the same edge one table earlier.

### `appointment.customer_id` is not constrained to the vehicle's owner

A-6 says the customer is derived from the vehicle's owner, and the service does exactly that
(AC-03). The schema, however, permits an appointment that references vehicle V and a customer who
does not own V: `customer_id` has a plain FK to `customer`, nothing ties it to
`vehicle.customer_id`. This is inconsistent with the treatment of INV-4/INV-5, which migration 0002
deliberately made database-enforceable through denormalised columns and composite FKs.

Decision: **do not add the composite FK.** An appointment should retain the customer as at booking
time. If the vehicle changes owner, historical appointments must keep the person who actually
brought the car in, and a composite FK `(vehicle_id, customer_id) → vehicle(id, customer_id)` would
either block the ownership transfer while any appointment references the vehicle, or — with
`ON UPDATE CASCADE` — retroactively rewrite those appointments. Both are worse than the risk of a
bad write, which only the service performs and which AC-03 tests. The alternative is recorded
here so the trade-off stays visible; if a second write path ever appears, the trigger-based
status-scoped approach above would fit this case as well.

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
  keys (see "INV-4 / INV-5 foreign keys and RESTRICT" above for the cost and the migration path).
* `appointment.customer_id` is intentionally *not* pinned to the vehicle's owner (see "Schema
  subtleties"); AC-03 is a service-layer guarantee.
