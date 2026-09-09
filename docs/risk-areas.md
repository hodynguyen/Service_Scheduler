# Risk areas

The highest-risk areas of this system, ordered by risk, with how each was verified. Items 3, 5
and 8 record design changes that came out of my manual review. Items 8–10 originated in a review
pass over the finished code; what that pass found and what changed is in the final entries of
`ai-collaboration-raw.md`.

1. **Exclusion constraints** — `internal/repository/postgres/migrations/0001_initial_schema.sql`.
   The correctness guarantee for INV-1..INV-3: `btree_gist`, `tstzrange(start_time, end_time, '[)')`,
   `WHERE (status = 'CONFIRMED')`, one constraint each for bay / technician / vehicle. Verified by
   `TestSchema_INV1..3_*`, which insert raw SQL and expect SQLSTATE 23P01 with the right constraint
   name, and by `TestSchema_INV1to3_ExclusionConstraintsArePartialOnConfirmed`, which reads the
   definitions back from `pg_constraint`. The `start_time < end_time` CHECK is part of this
   guarantee, not hygiene (ADR-0001 §Schema subtleties).
2. **Booking transaction, per-day advisory lock and retry budget** — `internal/service/scheduler.go`
   (`Book`, `assignAndInsert`) and `internal/repository/postgres/booking_tx.go` (`LockSchedulingDay`,
   `InsertAppointment`, `mapInsertError`). Lock order is idempotency key → day; the day lock only
   orders selection (CI showed exclusion-check deadlock cycles without it), the constraints remain
   the guarantee. Savepoint + retry on `ErrLostRace` (23P01 and 40P01). The budget is
   `max(3, min(qualified technicians, qualified bays)+1)`: the deterministic policy makes every
   losing request select the same next candidate, so exactly one competitor is retired per round
   and the budget is sufficient (ADR-0001). Decisions taken: a domain rejection is captured and the
   transaction still commits, so the idempotency record is written and a replay is stable; a
   *transient* outcome (budget exhausted while resources may still be free) is returned but not
   recorded, so a retry re-evaluates. Verified by `TestBooking_AC18_*` (20 competitors, one
   resource), `TestBooking_ConcurrentRequestsUseEveryFreeResourceWhenMoreThanThreeQualify` (four
   competitors, four resources, five runs) and `TestBooking_AC19_*`, all under `-race` and on CI.
3. **Error precedence** — `internal/domain/assign.go` checks resource availability before the
   vehicle (INV-3), so AC-18 holds literally (identical requests → `NO_AVAILABLE_RESOURCE`) and
   `VEHICLE_ALREADY_BOOKED` appears only when resources are free (AC-07). It was vehicle-first
   until my review; I changed it because the specification text wins over the arguably more
   actionable message. `internal/domain/hours.go` checks "in the past" before business hours.
   Verified by `TestAssign_AC18_ResourceConflictIsReportedBeforeVehicleConflict` and the
   identical-request variant of AC-18.
4. **Idempotency** — `internal/service/scheduler.go` (`replay`, fingerprint) and
   `booking_tx.go` (`LockIdempotencyKey`, `SaveIdempotencyRecord`). Advisory lock on
   `hashtext(dealership), hashtext(key)`; rejections are replayed; payload mismatch returns
   `IDEMPOTENCY_KEY_REUSED` (a code I added; now in requirements §10.2). Outcomes decided *before*
   the transaction (404/422) are not recorded, so their replay is re-evaluated. There is no purge
   job for expired rows; expiry is enforced on read. Verified by `TestBooking_AC19_*` (replay,
   mismatch, replayed rejection, eight concurrent requests with one key) and `TestIdempotency_*`
   (per-dealership scope, 24 h expiry, reuse of an expired row).
5. **Availability and INV-3** — `GET /availability` requires `vehicleId`, a change I made at review:
   without the vehicle, availability could offer a slot that booking then refuses on INV-3, so
   AC-22 ("any invariant") could not hold. `requirements.md` is at v1.1 (FR-1, §10.1) to match;
   spec, OpenAPI and code agree. Verified by `TestAvailability_AC22_*` at domain, service and E2E
   level, including that a different vehicle still sees the slots a busy vehicle loses.
6. **Load counting for BR-6** — `daySchedule` selects appointments whose *start* lies within the
   dealership-local calendar day (`domain.Date.Bounds`). Complete under INV-6 (no job spans
   midnight). This predicate, together with `status = 'CONFIRMED'`, is a deliberate exception to
   the "no business logic in SQL" rule I set at the outset: it is data scoping I chose to keep in
   the query rather than load every appointment ever made. Verified by
   `TestBooking_AC20_LoadIsCountedPerLocalDate`.
7. **Timezone handling** — `internal/domain/hours.go` (`Window`, `ValidateBookingTime`,
   `Date.Bounds`). Built with `time.Date(...)` in the dealership zone, which resolves wall-clock
   time across DST transitions. The seeded zone has no DST; DST days are untested. Zones whose DST
   starts at 00:00 (America/Santiago) shift `Bounds.Start` to 01:00 — only matters if opening hours
   start at midnight. Verified for the non-DST case by `TestBookingTime_*` and
   `TestBookingTime_A9_InstantIsEvaluatedInDealershipTimezone`.
8. **INV-4 / INV-5 database-enforced; INV-6 is not.** Migration
   `0002_enforce_skill_and_bay_type.sql` adds `required_skill_id` / `required_bay_type` on
   `appointment` and four composite FKs (ADR-0001). The service copies the two values from the
   loaded service type (`scheduler.go`, `newAppt`); the FKs make lying impossible, verified by
   `TestSchema_INV4_*`, `TestSchema_INV5_*` and
   `TestSchema_INV4_INV5_DenormalisedRequirementsMustMatchTheServiceType`. Decision: I kept the
   default RESTRICT semantics. Cost: changing a service type's requirement, decertifying a
   technician or retyping a bay is blocked while any appointment — including a cancelled one —
   references the old value. In a real workshop, revoking an EV high-voltage certification is
   safety-critical and cannot wait for the schedule to drain, so this is a known limitation; the
   migration path is recorded in ADR-0001 (a status-scoped check, or moving the check to the
   service layer with an audit trail). INV-6 (business hours) remains domain-only; a database check
   would need the hours table joined by weekday in the dealership's zone, which a constraint cannot
   express cleanly.
9. **Exact start times vs the 30-minute grid** — booking accepts any exact instant within hours
   (A-7); a booking at 09:07 consumes both the 09:00 and 09:30 availability slots. Decision: I do
   not reject non-zero seconds or off-grid minutes. A-7 makes the requested time exact, the
   availability grid is a presentation convenience, and rejecting off-grid starts would make FR-2
   depend on a granularity the specification never fixed. Recorded under A-7 in `requirements.md`.
10. **Operational defaults** — `middleware.RealIP` trusts `X-Forwarded-For` from any peer (only
    affects the `remote` log field; enable only behind a trusted proxy); `/readyz` pings the
    database, `/healthz` does not. CI is green on GitHub Actions (lint, unit + integration with
    testcontainers, image build) since the per-day lock landed; the two earlier red runs and their
    causes are in the log.
