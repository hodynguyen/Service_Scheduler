# Unified Service Scheduler

Backend for a car-dealership workshop: a Service Advisor asks *what* work and *when*; the system
decides *who* (technician) and *where* (bay), guarantees no double booking under concurrency, and
returns a confirmed appointment. Go 1.25 · chi · pgx · PostgreSQL 16 · Prometheus · OpenTelemetry.

* Specification (source of truth): [`docs/requirements.md`](docs/requirements.md)
* Design: [`docs/system-design.md`](docs/system-design.md) · ADRs: [`docs/adr/`](docs/adr/)
* API contract: [`openapi.yaml`](openapi.yaml)
* AI collaboration: [narrative below](#ai-collaboration-narrative), raw log
  [`docs/ai-collaboration-raw.md`](docs/ai-collaboration-raw.md), reviewer pointers
  [`docs/review-notes.md`](docs/review-notes.md)

## Quickstart (two commands)

```bash
docker compose up --build -d      # Postgres 16 + API on :8080; migrates and seeds on start
make test                         # unit + integration tests (integration uses testcontainers)
```

Then try it (see [cURL examples](#curl-examples)). `docker compose down -v` removes everything.

Requirements: Docker with Compose v2; Go 1.25+ only if you want to run tests or the server locally.

## What is guaranteed, and how

| Invariant | Enforcement |
|---|---|
| INV-1..3 no overlapping active appointments per bay / technician / vehicle | PostgreSQL **exclusion constraints** on `tstzrange(start,end,'[)')`, partial on `status='CONFIRMED'` (`btree_gist`). Application checks only produce precise error messages. |
| INV-4/5 technician holds the skill, bay has the type | Candidate filter in the domain (`domain.Assign`) |
| INV-6 inside business hours | `domain.ValidateBookingTime` in the dealership's IANA zone |
| INV-7 same dealership | Composite foreign keys `(resource_id, dealership_id)` |
| Atomic creation, idempotent retries (FR-4) | One transaction: advisory lock on the key → select → insert in a savepoint → record outcome |

Layering is `httpapi → service → domain`, with `repository/postgres` behind consumer-defined ports.
No ORM, no business logic in handlers or SQL, assignment policy behind `domain.AssignmentPolicy`.

## Endpoints

Base path `/api/v1`. Timestamps are ISO-8601 with an explicit offset; responses use the
dealership's offset. Full contract: [`openapi.yaml`](openapi.yaml).

| Method | Path | Purpose | Success | Errors |
|---|---|---|---|---|
| `GET` | `/availability?dealershipId&serviceTypeId&date[&vehicleId]` | Bookable start times (30-min steps) for a service type on a local date (FR-1) | `200` `{date, serviceType, availableSlots[]}` | `400 VALIDATION_ERROR`, `404 RESOURCE_NOT_FOUND` |
| `POST` | `/appointments` (header `Idempotency-Key`: UUID, optional) | Book with automatic technician/bay assignment (FR-2, FR-4) | `201` appointment + `Location` | `409 NO_AVAILABLE_RESOURCE {conflicting:[BAY\|TECHNICIAN]}`, `409 VEHICLE_ALREADY_BOOKED`, `422 OUTSIDE_BUSINESS_HOURS`, `422 SERVICE_EXCEEDS_CLOSING_TIME`, `422 START_TIME_IN_PAST`, `422 IDEMPOTENCY_KEY_REUSED`, `404`, `400` |
| `GET` | `/appointments/{id}` | Retrieve the full record (FR-3) | `200` same body as `201` | `404 RESOURCE_NOT_FOUND` |
| `GET` | `/healthz`, `/readyz`, `/metrics` | Liveness; readiness (database ping); Prometheus exposition | `200` (`503` when not ready) | — |

Error body: `{"code": "...", "message": "...", "conflicting": [...]?, "details": {field: reason}?}`.
Every response carries `X-Correlation-ID` (echoed from the request or generated).

## Seeded dealership

`Keyloop Motors Saigon` (`Asia/Ho_Chi_Minh`), Mon–Fri 08:00–17:00, Sat 08:00–12:00, Sunday closed.
Identifiers are fixed so examples are reproducible (`internal/repository/postgres/seed.go`).

| Kind | Id (…-4000-8000-…) | Notes |
|---|---|---|
| Dealership | `10000000-0000-4000-8000-000000000001` | |
| Service types | `3…01` Oil change 30 m (GENERAL) · `3…02` Wheel alignment 60 m (ALIGNMENT) · `3…03` EV battery diagnostic 90 m (EV) · `3…04` Transmission 180 m (GENERAL) | duration/skill/bay type come from here (BR-8) |
| Bays | `4…01` Bay 1 GENERAL · `4…02` Bay 2 GENERAL · `4…03` Alignment rig · `4…04` EV bay | EV work has exactly one bay (AC-18) |
| Technicians | `5…01` An (general, alignment) · `5…02` Binh (general, transmission) · `5…03` Chi (general, EV) · `5…04` Dung (general only) | Dung is free but unqualified for most work (AC-08) |
| Vehicles | `7…01` Toyota Camry (Mai) · `7…02` VinFast VF 8 (Quang) · `7…03` Honda CR-V (Linh) · `7…04` Ford Ranger (Linh) | customer is derived from the vehicle (A-6) |

## cURL examples

```bash
BASE=http://localhost:8080/api/v1
D=10000000-0000-4000-8000-000000000001   # dealership
ST=30000000-0000-4000-8000-000000000002  # wheel alignment (60 min)
V=70000000-0000-4000-8000-000000000001   # Toyota Camry

# 1. Availability for a Monday
curl -s "$BASE/availability?dealershipId=$D&serviceTypeId=$ST&date=2030-03-04" | jq .

# 2. Book 09:00 with an idempotency key (repeat the same command: same 201, same id)
curl -s -i -X POST "$BASE/appointments" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 8d2c8a52-1b1e-4c65-9b26-0000000000aa' \
  -d "{\"dealershipId\":\"$D\",\"vehicleId\":\"$V\",\"serviceTypeId\":\"$ST\",\"startTime\":\"2030-03-04T09:00:00+07:00\"}"

# 3. Retrieve it (id from the Location header)
curl -s "$BASE/appointments/<id>" | jq .

# 4. Conflict: another vehicle wants the only alignment rig/technician at 09:30 -> 409, conflicting [BAY, TECHNICIAN]
curl -s -X POST "$BASE/appointments" -H 'Content-Type: application/json' \
  -d "{\"dealershipId\":\"$D\",\"vehicleId\":\"70000000-0000-4000-8000-000000000002\",\"serviceTypeId\":\"$ST\",\"startTime\":\"2030-03-04T09:30:00+07:00\"}"

# 5. Adjacent slot is fine (half-open intervals): 10:00 -> 201 with the same technician and bay
curl -s -X POST "$BASE/appointments" -H 'Content-Type: application/json' \
  -d "{\"dealershipId\":\"$D\",\"vehicleId\":\"70000000-0000-4000-8000-000000000002\",\"serviceTypeId\":\"$ST\",\"startTime\":\"2030-03-04T10:00:00+07:00\"}"

# 6. Would finish after closing -> 422 SERVICE_EXCEEDS_CLOSING_TIME
curl -s -X POST "$BASE/appointments" -H 'Content-Type: application/json' \
  -d "{\"dealershipId\":\"$D\",\"vehicleId\":\"$V\",\"serviceTypeId\":\"$ST\",\"startTime\":\"2030-03-04T16:30:00+07:00\"}"

# 7. Sunday -> 422 OUTSIDE_BUSINESS_HOURS; malformed body -> 400 with per-field details
curl -s -X POST "$BASE/appointments" -H 'Content-Type: application/json' -d '{"dealershipId":"nope"}'

# 8. Metrics
curl -s localhost:8080/metrics | grep scheduler_bookings
```

Dates in the examples are in 2030 so they stay in the future; BR-5 rejects past start times.

## Development

```bash
make help              # all targets
make test-unit         # domain + handler tests, no Docker (-short)
make test              # everything; integration tests start Postgres 16 via testcontainers
make lint              # gofmt + go vet (+ golangci-lint if installed)
make run               # run locally against DATABASE_URL (defaults to the compose Postgres)
```

Configuration (env): `DATABASE_URL` (required), `HTTP_ADDR` (`:8080`), `APP_SEED` (`true` in
compose), `LOG_LEVEL`, `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT` (unset = spans not exported).

### Layout

```
cmd/server/                    entrypoint: config, migrate/seed, wiring, graceful shutdown
internal/domain/               pure rules: Interval, BusinessHours, Assign, AvailableSlots, policy
internal/service/              use cases (Availability, Book, GetAppointment), ports, retry, idempotency
internal/repository/postgres/  pgx repository, embedded migrations + seed, constraint mapping
internal/httpapi/              chi router, validation, DTOs, §10 error mapping
internal/observability/        slog JSON + correlation, Prometheus, OpenTelemetry
internal/testutil/pgtest/      shared testcontainers Postgres for integration tests
docs/                          requirements, system design, ADRs, AI log, review notes
```

## Testing strategy

Tests are named after acceptance criteria (`TestBooking_AC12_OneMinuteOverlapIsRejected`) so
coverage of the specification is greppable: `grep -rn "AC18" --include=*_test.go`.

| Layer | Package | What it proves | DB |
|---|---|---|---|
| Schema | `repository/postgres` (`schema_test.go`) | The database alone rejects overlapping CONFIRMED rows (SQLSTATE 23P01 with the right constraint), allows adjacency, ignores CANCELLED, enforces INV-7 | real |
| Domain | `domain` | AC-01..AC-17, AC-20..AC-24 on an in-memory schedule: half-open overlap, hours/DST-safe windows, qualification, deterministic policy, availability | none |
| Service | `service` | AC-01..AC-24 through the real service + repository, incl. **AC-18** (20 concurrent requests for the single EV bay/technician → exactly one 201, N−1 `NO_AVAILABLE_RESOURCE`, brute-force overlap check), a spare-resource race that must not be spuriously rejected, and AC-19 replay / scoping / mismatch / same-key concurrency | real |
| HTTP | `httpapi` | Handler contract against a fake service (validation cases, status mapping, body shape) and end-to-end through the router with the real stack, including a trace-span chain assertion | fake + real |
| Observability | `observability` | Correlation propagation, JSON log fields, metric names/labels, route-pattern labelling, server span naming and `traceparent` handling | none |

Integration tests start one Postgres 16 container per test binary (`internal/testutil/pgtest`) and
truncate mutable tables between tests; `-short` skips them. CI runs both modes on every push.

## AI Collaboration Narrative

This repository was produced with an AI coding agent (Claude) driving the implementation from
`docs/requirements.md`, under instructions to work autonomously, write acceptance-criteria tests
before the code they cover, commit in small conventional-commit steps, and keep a blunt running log.

**What the AI did well.** It kept the layering honest (no SQL or HTTP types leak into the service,
no rules in handlers or SQL), designed the database-first correctness model and the transaction
protocol around it, produced the concurrency and idempotency tests that actually exercise the
guarantees, and documented every decision the spec left open with the counter-argument attached.

**Where it needed a human, or would have.** The specification is silent on slot granularity, error
precedence, error body shape, what a replayed rejection should return, and whether AC-18's
"identical requests" share a vehicle. The agent chose and recorded each answer
(`docs/ai-collaboration-raw.md`); a reviewer should treat those as proposals. It also made one
genuine mistake that tests did not catch: the OpenTelemetry resource construction crashed the
service on start-up in Docker. Running the compose stack exposed it; a failing test was added and
then the fix — the kind of thing that only shows up when you run the thing.

**What to review first.** `docs/review-notes.md` lists seven places in priority order, starting
with the three exclusion constraints and the booking transaction.

**Process observations.** "Tests first" was followed at the commit level, but tests and
implementation were written minutes apart by the same author with the design already fixed, so the
red→green signal is weaker than it looks; judge the tests on content. The commit count (~62) overshot
the requested 25–40 because doc updates were committed per phase as instructed.
