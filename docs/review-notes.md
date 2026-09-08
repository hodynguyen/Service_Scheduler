# Review notes — where to look hardest

Maintained by the AI agent as the code grows. Ordered by risk.

1. `internal/repository/postgres/migrations/*.sql` — the three exclusion constraints are the
   correctness guarantee for INV-1..INV-3. Check: `btree_gist`, `tstzrange(start_time, end_time, '[)')`,
   `WHERE (status = 'CONFIRMED')`, one constraint each for bay, technician, vehicle.
2. Booking transaction (repository + service) — savepoint / retry logic around the insert, and the
   mapping from constraint name to the error code in requirements.md §10.
3. Business-hours evaluation in the dealership timezone (BR-4, BR-5) — DST edge cases were reasoned
   about, not exhaustively tested.
4. Idempotency (FR-4) — advisory-lock + fingerprint approach; failure outcomes are stored too.
(Items 2–4 are placeholders until those phases land; they will be made concrete.)
