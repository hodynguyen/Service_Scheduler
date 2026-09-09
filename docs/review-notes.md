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
3. **Error precedence** — `internal/domain/assign.go` checks resource availability before the
   vehicle (INV-3), so AC-18 holds literally (identical requests → `NO_AVAILABLE_RESOURCE`) and
   `VEHICLE_ALREADY_BOOKED` appears only when resources are free (AC-07). This was vehicle-first
   until the human review; `internal/domain/hours.go` still checks "in the past" before hours.
4. **Idempotency** — `internal/service/scheduler.go` (`replay`, fingerprint) and
   `booking_tx.go` (`LockIdempotencyKey`, `SaveIdempotencyRecord`). Advisory lock on
   `hashtext(dealership), hashtext(key)`; rejections are replayed; payload mismatch returns
   `IDEMPOTENCY_KEY_REUSED` (invented code). Outcomes decided *before* the transaction (404/422) are
   not recorded, so their replay is re-evaluated. No purge job for expired rows. Scope and expiry:
   `TestIdempotency_*` in `internal/repository/postgres`.
5. **Availability and INV-3** — `GET /availability` requires `vehicleId` (human decision) so every
   returned slot satisfies INV-1..INV-6 for that vehicle (AC-22). `requirements.md` was bumped to
   v1.1 (FR-1, §10.1) to match; spec, OpenAPI and code now agree.
6. **Load counting for BR-6** — `daySchedule` selects appointments whose *start* lies within the
   dealership-local calendar day (`domain.Date.Bounds`). Complete under INV-6 (no job spans midnight),
   but it is the one place a SQL predicate encodes a rule, together with `status = 'CONFIRMED'`.
7. **Timezone handling** — `internal/domain/hours.go` (`Window`, `ValidateBookingTime`,
   `Date.Bounds`). Built with `time.Date(...)` in the dealership zone. The seeded zone has no DST;
   DST days are untested. Zones whose DST starts at 00:00 (America/Santiago) shift `Bounds.Start` to
   01:00 — only matters if opening hours start at midnight.
8. **INV-4 / INV-5 now database-enforced; INV-6 is not.** Migration
   `0002_enforce_skill_and_bay_type.sql` adds `required_skill_id` / `required_bay_type` on
   `appointment` and four composite FKs (see ADR-0001). Check: the service copies the two values
   from the loaded service type (`scheduler.go`, `newAppt`); the FKs make lying impossible
   (`TestSchema_INV4_INV5_DenormalisedRequirementsMustMatchTheServiceType`). Operational cost:
   changing a service type's requirement, decertifying a technician or retyping a bay is blocked
   while appointments reference the old value — decide whether that RESTRICT behaviour is wanted.
   INV-6 (business hours) remains domain-only; a DB check would need the hours table joined by
   weekday and timezone, which is more than a constraint can express cleanly.
9. **Exact start times vs the 30-minute grid** — booking accepts any exact instant (A-7); a booking at
   09:07 consumes both the 09:00 and 09:30 availability slots. Consistent with the spec, but decide
   whether non-zero seconds should be rejected.
10. **Operational defaults** — `middleware.RealIP` trusts `X-Forwarded-For` from any peer (only
    affects the `remote` log field); `/readyz` pings the database, `/healthz` does not. CI is green
    on GitHub Actions (lint, unit + integration with testcontainers, image build) since the per-day
    lock landed; the two earlier red runs and their causes are in the log.
