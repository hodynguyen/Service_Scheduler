# Service Scheduler — engineering constraints

Source of truth: `docs/requirements.md`. Never contradict it. IDs (FR-*, BR-*, INV-*, A-*, AC-*)
are referenced from tests, commits and docs.

## Non-negotiable

- Go 1.24+, PostgreSQL 16, `chi` router, `pgx` v5, `testcontainers-go` for integration tests.
  **No ORM.** Keep the dependency list minimal; justify every new module.
- Layering: `internal/http` (handler) -> `internal/service` -> `internal/repository/postgres`.
  Business logic lives in `internal/domain` (pure) and is orchestrated by the service.
  **No business logic in handlers or SQL.**
- INV-1..INV-3 (no overlapping CONFIRMED appointments per bay / technician / vehicle) are
  enforced by PostgreSQL **exclusion constraints on `tstzrange(start_time, end_time, '[)')`**,
  partial on `status = 'CONFIRMED'`, using `btree_gist`. Application-level checks exist only to
  produce good error messages — never as the correctness guarantee.
- Time is stored as `timestamptz` (UTC instants). Intervals are half-open `[start, end)`.
  Business hours are evaluated in the dealership's IANA timezone.
- The assignment policy (BR-6: least-loaded that day, ties by ascending id) is deterministic and
  sits behind the `domain.AssignmentPolicy` interface.

## Conventions

- Tests are named after acceptance criteria: `TestBooking_AC12_OneMinuteOverlapIsRejected`.
- Conventional Commits (`feat:`, `test:`, `docs:`, `chore:`, `refactor:`), one logical change per
  commit, tests committed before the implementation they cover, body explains WHY with AC/INV IDs.
- Errors crossing the service boundary are `*domain.Error` with a `Code` from requirements.md §10.

## Commands

- `make test` (unit + integration, needs Docker), `make test-unit`, `make lint`, `make run`
- `docker compose up` starts Postgres 16 and the API (migrates and seeds on start)

## Running log

After each phase append to `docs/ai-collaboration-raw.md` (blunt, specific, no flattery) and keep
`docs/review-notes.md` current with the places a human should review most carefully.
