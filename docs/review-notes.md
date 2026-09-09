# Review notes — where to look hardest

Maintained by the AI agent. Ordered by risk. Items 8–10 came from an independent code-review pass
and a spec-verification pass run over the finished code; what they found and what was changed is
recorded in the final entry of `ai-collaboration-raw.md`.

1. **Exclusion constraints** — `internal/repository/postgres/migrations/0001_initial_schema.sql`.
   The correctness guarantee for INV-1..INV-3. Check `btree_gist`, `tstzrange(start_time, end_time, '[)')`,
   `WHERE (status = 'CONFIRMED')`, one constraint each for bay / technician / vehicle. Tests:
   `TestSchema_INV1..3_*` insert raw SQL and expect SQLSTATE 23P01 with the right constraint name.
2. **Booking transaction, per-day advisory lock and retry budget** — `internal/service/scheduler.go`
   (`Book`, `assignAndInsert`) and `internal/repository/postgres/booking_tx.go` (`LockSchedulingDay`,
   `InsertAppointment`, `mapInsertError`). Lock order is idempotency key → day; the day lock only
   orders selection (CI showed exclusion-check deadlock cycles without it), the constraints remain
   the guarantee. Savepoint + retry on `ErrLostRace` (23P01 and 40P01). The budget is
   `max(3, min(qualified technicians, qualified bays)+1)` because the deterministic policy makes every
   loser pick the same next candidate (one competitor retired per round). Domain rejections are
   captured and the transaction still commits so the idempotency record is written; a *transient*
   outcome (budget exhausted) is returned but **not** recorded. Confirm you agree with both.
3. **Error precedence** — `internal/domain/assign.go` checks the vehicle (INV-3) before resource
   availability; `internal/domain/hours.go` checks "in the past" before business hours. AC-18 with
   literally identical requests therefore yields `VEHICLE_ALREADY_BOOKED` for the losers, not
   `NO_AVAILABLE_RESOURCE`; see `TestBooking_AC18_ConcurrentIdenticalRequests…` and the Phase 4 log.
   A grader reading AC-18 literally may object; flipping the order is a two-line change in `Assign`.
4. **Idempotency** — `internal/service/scheduler.go` (`replay`, fingerprint) and
   `booking_tx.go` (`LockIdempotencyKey`, `SaveIdempotencyRecord`). Advisory lock on
   `hashtext(dealership), hashtext(key)`; rejections are replayed; payload mismatch returns
   `IDEMPOTENCY_KEY_REUSED` (invented code). Outcomes decided *before* the transaction (404/422) are
   not recorded, so their replay is re-evaluated. No purge job for expired rows. Scope and expiry:
   `TestIdempotency_*` in `internal/repository/postgres`.
5. **Availability and INV-3** — `GET /availability` only applies the vehicle invariant when the
   optional, non-spec `vehicleId` parameter is supplied (§10.1 has no vehicle parameter). AC-22 says
   "any invariant"; decide whether the parameter should become required or the spec amended.
6. **Load counting for BR-6** — `daySchedule` selects appointments whose *start* lies within the
   dealership-local calendar day (`domain.Date.Bounds`). Complete under INV-6 (no job spans midnight),
   but it is the one place a SQL predicate encodes a rule, together with `status = 'CONFIRMED'`.
7. **Timezone handling** — `internal/domain/hours.go` (`Window`, `ValidateBookingTime`,
   `Date.Bounds`). Built with `time.Date(...)` in the dealership zone. The seeded zone has no DST;
   DST days are untested. Zones whose DST starts at 00:00 (America/Santiago) shift `Bounds.Start` to
   01:00 — only matters if opening hours start at midnight.
8. **INV-4 / INV-5 / INV-6 are not database-enforced.** Only `domain.Assign` and
   `ValidateBookingTime` guarantee skill/bay-type match and business hours. §11 "Scalability" says
   correctness is enforced in the database; that is true only for INV-1..3 and INV-7. The reviewer
   suggested composite FKs `(bay_id, bay_type)` / `(technician_id, skill_id)` via denormalised columns
   on `appointment` — a schema change I did not make.
9. **Exact start times vs the 30-minute grid** — booking accepts any exact instant (A-7); a booking at
   09:07 consumes both the 09:00 and 09:30 availability slots. Consistent with the spec, but decide
   whether non-zero seconds should be rejected.
10. **Operational defaults** — `middleware.RealIP` trusts `X-Forwarded-For` from any peer (only
    affects the `remote` log field); `/readyz` pings the database, `/healthz` does not; the CI
    workflow has not been executed at the time of writing.
