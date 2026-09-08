# Review notes — where to look hardest

Maintained by the AI agent as the code grows. Ordered by risk.

1. **Exclusion constraints** — `internal/repository/postgres/migrations/0001_initial_schema.sql`.
   The correctness guarantee for INV-1..INV-3. Check `btree_gist`, `tstzrange(start_time, end_time, '[)')`,
   `WHERE (status = 'CONFIRMED')`, one constraint each for bay / technician / vehicle. Tests:
   `TestSchema_INV1..3_*` insert raw SQL and expect SQLSTATE 23P01 with the right constraint name.
2. **Booking transaction** — `internal/service/scheduler.go` (`Book`, `assignAndInsert`) and
   `internal/repository/postgres/booking_tx.go` (`InsertAppointment`, `mapInsertError`).
   Savepoint + bounded retry on `ErrLostRace`; domain rejections are captured and the transaction
   still commits so the idempotency record is written. Confirm you agree that a rejection should
   commit, and that 3 attempts is a sensible bound.
3. **Error precedence** — `internal/domain/assign.go` checks the vehicle (INV-3) before resource
   availability; `internal/domain/hours.go` checks "in the past" before business hours. AC-18 with
   literally identical requests therefore yields `VEHICLE_ALREADY_BOOKED` for the losers, not
   `NO_AVAILABLE_RESOURCE`; see `TestBooking_AC18_ConcurrentIdenticalRequests…` and the Phase 4 log.
4. **Idempotency** — `internal/service/scheduler.go` (`replay`, fingerprint) and
   `booking_tx.go` (`LockIdempotencyKey`, `SaveIdempotencyRecord`). Advisory lock on
   `hashtext(dealership), hashtext(key)`; rejections are replayed; payload mismatch returns
   `IDEMPOTENCY_KEY_REUSED` (invented code). No purge job for expired rows.
5. **Load counting for BR-6** — `daySchedule` selects appointments whose *start* lies within the
   dealership-local calendar day (`domain.Date.Bounds`). An appointment can never span midnight
   under INV-6, so this is complete, but it is the one place the SQL predicate encodes a rule.
6. **Timezone handling** — `internal/domain/hours.go` (`Window`, `ValidateBookingTime`). Built with
   `time.Date(...)` in the dealership zone. The seeded zone has no DST; DST days are untested.
7. **INV-4 / INV-5 are not database-enforced.** Only the candidate filter in `domain.Assign`
   guarantees skill and bay-type match. A trigger or a check via composite FK on
   `(bay_id, bay_type)` would harden this if you consider it in scope.
