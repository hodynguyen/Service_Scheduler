# ADR-0003 — Time modelling

**Status:** accepted · **Date:** 2026-09-09 · **Relates to:** BR-4, BR-5, INV-6, A-5, A-9, AC-10..AC-16, AC-23

## Context

Appointments are absolute instants, but business hours, "today" and "in the past" are local
concepts of a dealership. Clients may send any offset. Adjacent jobs must not conflict, and a job
finishing exactly at closing time must be allowed. Daylight-saving transitions must not shift
opening hours.

## Decision

* **Instants in UTC, stored as `timestamptz`.** `start_time`/`end_time` are absolute; the database
  compares instants. Overlap uses `tstzrange(start, end, '[)')` (ADR-0001).
* **Half-open intervals `[start, end)` everywhere.** `domain.Interval.Overlaps` is
  `a.Start < b.End && b.Start < a.End` — the same predicate as the range operator `&&` on `[)`
  ranges, so the advisory check and the guarantee agree. An appointment ending at 10:00 does not
  conflict with one starting at 10:00 (AC-10/11); a one-minute overlap does (AC-12).
* **Dealership carries an IANA timezone** (`dealership.timezone`, e.g. `Asia/Ho_Chi_Minh`).
  Business hours are stored per weekday as local wall-clock times (`business_hours.opens_at /
  closes_at time`, weekday 0 = Sunday). A missing weekday means closed.
* **Windows built with `time.Date` in the dealership zone.** `BusinessHours.Window(date, loc)`
  produces absolute `[open, close)` for a civil date; wall-clock construction keeps 08:00 at 08:00
  across DST changes. The open interval is half-open too: starting *at* closing time is outside
  hours, ending *at* closing time is fine.
* **Validation order (BR-5 then BR-4):** `start < now` → `START_TIME_IN_PAST`; closed day or start
  outside `[open, close)` → `OUTSIDE_BUSINESS_HOURS`; `start + duration > close` →
  `SERVICE_EXCEEDS_CLOSING_TIME`. "Now" is injectable (`service.WithClock`) so tests are stable.
* **API.** Input is ISO-8601 with an explicit offset (`Z` accepted) and parsed as an instant.
  Output is always rendered in the dealership's offset; the repository attaches the dealership's
  `*time.Location` to the times it returns. `date` on availability is the dealership-local
  calendar date.
* **"That date" for load (BR-6)** is the local calendar day `[00:00, 24:00)` of the requested
  start (`Date.Bounds`).
* **Slot granularity** for availability is 30 minutes (`domain.DefaultSlotGranularity`); the
  spec does not fix it. Booking accepts any exact start within hours (A-7).

## Alternatives considered

| Alternative | Why not |
|---|---|
| `timestamp without time zone` in local time | Ambiguous across DST and across dealerships; comparisons in SQL become wrong once a second zone appears |
| Store a UTC offset per dealership instead of an IANA name | Breaks on DST; the offset is a property of an instant, not of a place |
| Closed intervals with a 1-minute gap | Adjacency would need special-casing everywhere; half-open is the standard model and matches `[)` ranges |
| Server-local time / `time.Now()` inside the domain | Untestable and wrong for a multi-region deployment; the clock is injected |
| Storing business hours as UTC instants per day | Requires generating rows per date; weekly local hours are what a dealership actually publishes |

## Consequences

* All comparisons are instant-based; only rendering and hour lookup touch the zone.
* DST is handled by construction but the seeded zone has none; a Europe/London fixture on a
  transition day would be the next test to add.
* A job can never span local midnight under INV-6, which is what makes "appointments starting
  within the day" a complete definition of the day's schedule.
* Clients get consistent `+07:00` timestamps regardless of what they sent, which surprises some
  clients but keeps responses comparable and the workshop's clock visible.
