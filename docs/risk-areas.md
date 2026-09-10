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
   the guarantee. Savepoint + retry on `ErrLostRace` (23P01 and 40P01). The budget is exactly
   `max(3, min(qualified technicians, qualified bays)+1)`, computed once from the first schedule
   load and never extended: the deterministic policy makes every losing request select the same
   next candidate, so exactly one competitor is retired per round (ADR-0001). An earlier revision
   grew the budget per deadlock victim under an undocumented ceiling; that was removed once the
   per-day lock made the deadlock unreachable through any current writer.
   Decisions taken: a domain rejection is captured and the transaction still commits, so the
   idempotency record is written and a replay is stable; a *transient* outcome — the budget ran out
   while selection on fresh data still succeeds — is reported as `503 CONTENTION` with `Retry-After`
   and is deliberately **not** recorded, so a retry with the same key re-evaluates. Verified by
   `TestBooking_AC18_*` (20 competitors, one resource),
   `TestBooking_ConcurrentRequestsUseEveryFreeResourceWhenMoreThanThreeQualify` (four competitors,
   four resources, five runs), `TestBooking_AC19_*`, and the `TestBooking_Contention*` /
   `TestBooking_BudgetIsQualifiedResourcesPlusOneWithAFloorOfThree` unit tests, which drive the
   contention branch through an in-memory repository so it does not depend on timing.
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

## Known open items

Found by an independent consistency review of the finished repository (2026-09-10) and left
unfixed deliberately. Each is recorded with what it costs to close, so the omissions are choices
rather than oversights. Nothing here affects the invariants; they are accuracy and polish defects.

### Documents that contradict the code

| Item | Where | Cost |
|---|---|---|
| The README's traceability table claims the `domain` package covers AC-01..AC-17. It has no AC-03 and no AC-17 test and structurally cannot: the domain models neither vehicle ownership nor appointment status. Both are covered at service and schema level. | `README.md:141` | 2 min |
| `CLAUDE.md` names the handler package `internal/http`; it was renamed to `internal/httpapi` so it stops shadowing `net/http`. | `CLAUDE.md:10` | 1 min |
| The sequence diagram glosses the retry budget as "budget = qualifying resources"; it is `max(3, min(qualified technicians, qualified bays) + 1)`. | `docs/system-design.md:87` | 2 min |
| Three places still say the specification does not fix the slot granularity. Since requirements v1.1, A-7 does. | `docs/adr/0003-time-modeling.md:37`, `docs/risk-areas.md:84`, `internal/domain/availability.go:6` | 5 min |
| The raw log states that `openapi.yaml` documents `/healthz`. It documents three paths and no health endpoint. This is a present-tense claim about a file in the repository, so the "unedited log" exemption does not cover it — the correction belongs in a new dated entry, not an edit. | `docs/ai-collaboration-raw.md:247` | 3 min |
| The raw log contradicts itself on the commit count (68 vs 63, three lines apart) and its "68 commits on `origin/main`" is stale (now 95). Same constraint: correct by appending. | `docs/ai-collaboration-raw.md:314, 317, 343` | 3 min |
| `README.md:36` and `CLAUDE.md:12` state "no business logic in SQL" without qualification; item 6 above is the acknowledged exception. | `README.md:36`, `CLAUDE.md:12` | 2 min |
| "Milliseconds each" for lock-serialised bookings is unbenchmarked; the log itself records that no benchmark exists. | `docs/system-design.md:204`, `docs/adr/0001…:135` | 3 min |
| The README narrative attributes the retry-budget finding to both review passes (one found it), says "the suite asserts" a check the suite does not assert in that exact form, glosses "all seven phases" with six, and says CI runs on every push when the workflow triggers on `main` and pull requests only. | `README.md:187, 236, 166, 147` | 8 min |
| AC-07 in the specification is worded unconditionally and is false under the settled resource-before-vehicle precedence. The spec is the only document that does not record that rule. | `docs/requirements.md:300` | 5 min |

### Behaviour that no document describes

| Item | Where | Cost |
|---|---|---|
| A panic returns a bare `500` with an empty body, because chi's `Recoverer` writes only a status. `requirements.md` §10 and `openapi.yaml` both promise every error body carries `code` and `message`. A custom recoverer closes it. | `internal/httpapi/router.go:59` | 30 min |
| A request that trips `middleware.Timeout` can likewise emit a bare `504`, and a client-cancelled request returns `200` with an empty body (`errors.go` deliberately writes nothing once the client is gone). Neither shape is documented. | `internal/httpapi/router.go:60`, `internal/httpapi/errors.go:47` | included above |
| `405` and `504` appear in the OpenAPI error enum but are attached to no path; `/healthz`, `/readyz` and `/metrics` are absent from the contract entirely, and `/readyz`'s `503` body matches no schema. | `openapi.yaml` | 10 min |
| `details` is documented as a map of field name to reason, but one key is the `Idempotency-Key` header name, and a `VALIDATION_ERROR` raised in the service layer carries no `details` at all. | `openapi.yaml:216`, `internal/httpapi/handlers.go:88` | 5 min |

### Durability and operations

| Item | Where | Cost |
|---|---|---|
| `idempotency_key` grows without bound. FR-4 says keys are "retained for 24 hours"; expiry is enforced on read only, and the `expires_at` index supports a sweep that does not exist. Rows are reclaimed only when the same key recurs. | `migrations/0001:140`, `booking_tx.go` | 10 min to document, ~1 h to implement with a scheduled delete |
| A `23503` from the INV-4/INV-5 foreign keys is classified as "a bug, not contention" and returns 500. Decertifying a technician between the schedule read and the insert is legitimate concurrency and should retry. The constraint names are already exported for exactly this. | `internal/repository/postgres/booking_tx.go` | 15 min |
| INV-6 has no database enforcement — see item 8 above. Unchanged. | — | — |

### Tests

| Item | Where | Cost |
|---|---|---|
| `TestHTTP_AC24` is named for the acceptance criterion but stubs the service to return an empty slice, so it asserts JSON rendering, not a fully booked day. No test joins "full day" to "200 with `[]`" across the HTTP boundary. | `internal/httpapi/handlers_test.go` | 5 min |
| `TestAssign_AC18_ResourceConflictIsReportedBeforeVehicleConflict` is single-threaded and tests precedence, not the concurrency criterion. The name should not carry AC-18, which is properly covered at service level. | `internal/domain/assign_test.go` | 2 min |
| `TestBooking_AC09` does not isolate the wrong-bay-type failure: the only EV technician is busy at the same instant, which is why its assertion was weakened to `Conflicting[0]`. | `internal/service/scheduler_integration_test.go` | 10 min |
| A dead assertion, `if !errors.Is(err, err)`, guards an unreachable `t.Fatal` purely to keep an import alive. | `internal/service/scheduler_integration_test.go:682` | 1 min |
| AC-19's per-dealership scoping and 24-hour expiry are asserted against `BookingTx` directly, never through `Scheduler.Book`. | `internal/repository/postgres/idempotency_test.go` | 15 min |

### Housekeeping

`part1.txt` and `part2.txt` are untracked concatenations of the repository's own documents. A clone
is clean; a zip of the working directory is not. Cost: 1 min.

### Not verifiable without more work

That `ROLLBACK TO SAVEPOINT` recovers a transaction from a `40P01` deadlock on PostgreSQL 16 is
assumed by the retry path and was reasoned about, not tested; two `psql` sessions would settle it.
The advisory-lock ordering is total up to `hashtext` injectivity — a cross-collision between a key
string and a `"day:…"` string could in principle deadlock, at roughly 2⁻⁶⁴, surfacing as a clean
500. The 200 ms p95 requirement has no benchmark anywhere in the repository.
