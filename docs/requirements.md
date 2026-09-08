# Unified Service Scheduler — Requirements Specification

**Scenario A — Keyloop Technical Assessment**
**Version:** 1.0
**Status:** Baseline for implementation
**Implemented layer:** Backend (RESTful API + persistent database). The client layer is stubbed via an OpenAPI contract and cURL examples.

---

## 1. Purpose

This document defines *what* the system must do, before any decision about *how* it is built. Architecture, technology choices and data flow are covered separately in [`system-design.md`](./system-design.md); the reasoning behind individual technical decisions lives in [`adr/`](./adr/).

The original brief is deliberately ambiguous. Every ambiguity found has been resolved into an explicit assumption in §9, each with its rationale and the cost of being wrong.

---

## 2. Domain context

A car dealership runs two businesses: selling vehicles and servicing them. The service workshop is typically the higher-margin side, and its economics are driven by a single number — **bay utilisation**, the proportion of available labour hours that are actually sold.

A workshop hour is perishable inventory. An empty bay at 10:00 cannot be sold later; that revenue is gone, in the same way an empty airline seat is gone once the aircraft departs.

This creates two opposing pressures:

- Book **densely**, so capacity is not wasted.
- Book **correctly**, so the shop floor does not collapse — no double-booked bays, no jobs assigned to technicians who are not certified to perform them.

Manual booking (paper diaries, shared spreadsheets, phone calls) fails at the second. Two staff members can accept the same 09:00 slot; a job can be booked when the only qualified technician is on leave. This system exists to make the second pressure a guarantee rather than a hope.

---

## 3. Actors

| Actor | Role | Interacts with the system? |
|---|---|---|
| **Customer** | Vehicle owner. Describes a problem or requests scheduled maintenance. | No — always via the Service Advisor |
| **Service Advisor** | Translates the customer's description into a concrete `ServiceType`, then books it. | Yes — primary API consumer |
| **Technician** | Performs the work. Has skills and certifications. | No (v1) — a scheduled resource |
| **Workshop Controller** | Historically assigns jobs to bays and technicians. | No — this system replaces that function |

**Critical boundary.** The Service Advisor supplies domain judgement: turning *"there's a vibration in the steering at speed"* into `ServiceType: Wheel alignment and balancing`. Everything downstream of that choice — duration, required skill, required bay type — is derived by the system, not estimated by a human.

The Service Advisor therefore chooses **what work** and **when**. The system chooses **who** performs it and **where**.

---

## 4. Glossary

| Term | Definition |
|---|---|
| **Dealership** | A physical site owning bays and employing technicians. Has an IANA timezone and weekly business hours. |
| **Service Bay** | A physical working position. Typed — not every bay can perform every service. |
| **Bay type** | Capability class of a bay: `GENERAL`, `ALIGNMENT`, `EV`. |
| **Technician** | A person employed by a dealership, holding zero or more skills. |
| **Skill** | A capability or certification (e.g. `EV_HIGH_VOLTAGE`, `TRANSMISSION`). Certain work is legally restricted to holders. |
| **Service Type** | A catalogue entry defining a unit of work: its standard duration, the skill it requires, and the bay type it requires. |
| **Appointment** | A confirmed reservation binding a customer, vehicle, technician, bay and time range. |
| **Time range** | A half-open interval `[start, end)` — an appointment ending at 10:00 does not conflict with one starting at 10:00. |
| **Active appointment** | An appointment with status `CONFIRMED`. Cancelled appointments do not reserve resources. |

---

## 5. Scope

### 5.1 In scope

- Querying availability for a given service type on a given date
- Booking an appointment with automatic resource assignment
- Retrieving a single appointment
- Enforcement of all invariants in §8 under concurrent load
- Seeded reference data for one dealership

### 5.2 Out of scope

Deliberately excluded to keep the implementation deep rather than wide. Each is a real requirement in a production system.

| Excluded | Rationale | How it would be added |
|---|---|---|
| Authentication and authorisation | Adds no scheduling complexity | Middleware; `Appointment.createdBy` field |
| Multi-tenancy | One dealership already exercises every invariant | Tenant scoping in the repository layer |
| Rescheduling and cancellation | `status` column exists; endpoints add CRUD surface, not depth | Two endpoints; invariants unchanged |
| Notifications | Orthogonal to scheduling correctness | Outbox pattern on appointment creation |
| Technician shifts and leave | Would triple the reference data for one extra filter clause | `technician_availability` table joined into the candidate query |
| Parts availability | A second, independent constraint domain | Would extend the assignment step, not replace it |
| Courtesy vehicles, quotations, waitlists | Adjacent business processes | Separate bounded contexts |

---

## 6. Functional requirements

Each requirement traces back to the original brief (R1–R3).

### FR-1 — Query availability *(supports R2)*

Given a dealership, a service type and a date, the system returns the set of start times at which the appointment could be booked.

- Slots are generated at a fixed granularity (§9, A-7) within the dealership's business hours for that day.
- A slot is returned only if it satisfies every invariant in §8 at the moment of the query.
- The response exposes **start times only**. It does not reveal which technician or bay would be assigned — resource allocation is internal to the workshop.
- Availability is advisory. It carries no reservation and may be stale by the time a booking is attempted.

### FR-2 — Book an appointment *(implements R1, R2, R3)*

Given a vehicle, a service type, a dealership and a desired start time, the system either creates a confirmed appointment or rejects the request with a specific reason.

- The client does **not** supply a technician or a bay.
- End time is computed as `start + ServiceType.durationMinutes`.
- The customer is derived from the vehicle's owner and is not accepted from the client (§9, A-6).
- The system selects a technician and a bay according to the policy in BR-6.
- Creation is atomic: either an appointment satisfying all invariants exists, or nothing is written.

### FR-3 — Retrieve an appointment *(supports R3)*

Given an appointment identifier, the system returns the full record including the assigned technician, bay, customer, vehicle, service type and time range.

### FR-4 — Idempotent booking

A booking request carrying an `Idempotency-Key` header that has already been processed returns the original result rather than creating a second appointment.

- Protects against client retries after a network timeout.
- Keys are scoped per dealership and retained for 24 hours.

---

## 7. Business rules

| ID | Rule |
|---|---|
| **BR-1** | An appointment occupies exactly one bay and exactly one technician for its entire duration. |
| **BR-2** | The assigned technician must hold the skill required by the service type. |
| **BR-3** | The assigned bay must be of the bay type required by the service type. |
| **BR-4** | The appointment must fall entirely within the dealership's business hours for that weekday — a job may not start before opening or finish after closing. |
| **BR-5** | An appointment may not start in the past, evaluated in the dealership's local timezone. |
| **BR-6** | Where several technicians qualify, the one with the fewest assigned minutes on that date is chosen; ties are broken by ascending identifier. The same rule applies to bays. The policy is deterministic. |
| **BR-7** | Technician, bay and vehicle must all belong to the same dealership as the appointment. |
| **BR-8** | Duration is determined solely by the service type. Clients cannot supply or override it. |
| **BR-9** | Cancelled appointments release their resources immediately and never block a new booking. |

---

## 8. System invariants

These hold at all times, under any sequence of concurrent operations. They are the correctness contract of the system.

| ID | Invariant |
|---|---|
| **INV-1** | No bay is assigned to two active appointments whose time ranges overlap. |
| **INV-2** | No technician is assigned to two active appointments whose time ranges overlap. |
| **INV-3** | No vehicle appears in two active appointments whose time ranges overlap. |
| **INV-4** | Every assigned technician holds the skill required by the appointment's service type. |
| **INV-5** | Every assigned bay matches the bay type required by the appointment's service type. |
| **INV-6** | Every active appointment lies wholly within its dealership's business hours. |
| **INV-7** | Technician, bay and vehicle share the appointment's dealership. |

**Notes.**

INV-3 is easy to miss — a vehicle cannot occupy two bays simultaneously, so it is a scheduling resource in its own right, not merely a piece of associated data.

The word *active* in INV-1 to INV-3 is load-bearing. Enforcement must be scoped to `status = 'CONFIRMED'`, otherwise a cancelled appointment would permanently block its slot.

Enforcement of INV-1 to INV-3 must survive concurrent requests. An application-level check followed by an insert is a read–modify–write race and is not sufficient on its own. See ADR-0001.

---

## 9. Assumptions

Resolutions of ambiguity in the original brief. Each states what was assumed, why, and what changes if the assumption proves wrong.

| ID | Ambiguity | Assumption | Rationale | If wrong |
|---|---|---|---|---|
| **A-1** | Where does service duration come from? | Fixed per `ServiceType` | Manufacturers publish standard labour times; advisors look them up rather than estimating | Becomes a lookup on `(serviceType, vehicleModel)`; the scheduling core is untouched |
| **A-2** | How is "qualified" modelled? | One required skill per service type; technicians hold many skills | Sufficient to produce a meaningful rejection case without inflating reference data | Becomes a required *set*; only the candidate query changes |
| **A-3** | Can any bay perform any service? | No — bays are typed, service types declare a required type | Alignment rigs and EV bays are genuinely distinct in a real workshop | Drop the filter; strictly simplifying |
| **A-4** | Which technician is chosen when several qualify? | Least-loaded that day, ties by identifier | Deterministic (therefore testable) while modelling a real objective — load balancing | Policy sits behind an interface and is replaceable |
| **A-5** | Is there a buffer between jobs? | None in v1; intervals are half-open `[start, end)` | Simplest correct model; adjacent bookings genuinely do not conflict | Widen the stored interval; the constraint is unchanged |
| **A-6** | Where does the customer come from? | Derived from the vehicle's owner | R1 supplies a vehicle but R3 requires a customer; deriving prevents contradictory state | Accept and validate an explicit customer reference |
| **A-7** | Is the desired time exact or approximate? | Exact. Unavailable means rejection, not a nearest-match suggestion | The brief checks availability *before confirming*, implying a specific requested time | `FR-1` already gives the client the bookable set to choose from |
| **A-8** | Do technicians have individual shifts? | No — they inherit dealership business hours | Avoids tripling reference data for one additional filter | Add a `technician_availability` table to the candidate query |
| **A-9** | How are timezones handled? | Instants stored in UTC; each dealership carries an IANA timezone; the API uses offset-aware ISO-8601 | Business hours are local concepts; instants are absolute | N/A — this is the correct model |
| **A-10** | Is a service advisor recorded? | No — out of scope with authentication | Cannot attribute a booking without authenticated identity | Add `Appointment.createdBy` |

---

## 10. API contract

Base path: `/api/v1`. All timestamps are ISO-8601 with an explicit offset.

### 10.1 `GET /availability`

**Query parameters:** `dealershipId`, `serviceTypeId`, `date` (`YYYY-MM-DD`, local to the dealership)

**200 OK**
```json
{
  "date": "2026-09-15",
  "serviceType": { "id": "...", "name": "Wheel alignment", "durationMinutes": 60 },
  "availableSlots": ["2026-09-15T09:00:00+07:00", "2026-09-15T10:30:00+07:00"]
}
```

An empty `availableSlots` array is a valid 200 response, not an error.

### 10.2 `POST /appointments`

**Headers:** `Idempotency-Key` (optional, UUID)

**Request**
```json
{
  "dealershipId": "...",
  "vehicleId": "...",
  "serviceTypeId": "...",
  "startTime": "2026-09-15T09:00:00+07:00"
}
```

**201 Created**
```json
{
  "id": "...",
  "status": "CONFIRMED",
  "startTime": "2026-09-15T09:00:00+07:00",
  "endTime": "2026-09-15T10:00:00+07:00",
  "customer": { "id": "...", "name": "..." },
  "vehicle": { "id": "...", "vin": "...", "model": "..." },
  "serviceType": { "id": "...", "name": "..." },
  "technician": { "id": "...", "name": "..." },
  "bay": { "id": "...", "name": "...", "bayType": "ALIGNMENT" }
}
```

**Error responses**

| Status | Code | Meaning |
|---|---|---|
| 409 | `NO_AVAILABLE_RESOURCE` | No qualifying technician and/or bay is free. The `conflicting` array indicates which — `["BAY"]`, `["TECHNICIAN"]`, or both |
| 409 | `VEHICLE_ALREADY_BOOKED` | The vehicle has an overlapping active appointment (INV-3) |
| 422 | `OUTSIDE_BUSINESS_HOURS` | Start time falls outside opening hours (BR-4) |
| 422 | `SERVICE_EXCEEDS_CLOSING_TIME` | Start is valid but the job would finish after closing (BR-4) |
| 422 | `START_TIME_IN_PAST` | BR-5 |
| 404 | `RESOURCE_NOT_FOUND` | Unknown dealership, vehicle or service type |
| 400 | `VALIDATION_ERROR` | Malformed payload |

Distinguishing *no bay* from *no technician* is deliberate: it lets an advisor tell a customer something more useful than "unavailable".

### 10.3 `GET /appointments/{id}`

**200 OK** — same body as the 201 above. **404** if unknown.

---

## 11. Non-functional requirements

| Attribute | Requirement |
|---|---|
| **Correctness** | Invariants hold under concurrent booking. Demonstrated by a test that fires N simultaneous requests for one slot and asserts exactly one succeeds. |
| **Performance** | Booking completes within 200 ms at p95 under moderate load. Availability queries are read-only and index-supported. |
| **Reliability** | All writes for a booking occur in a single transaction. Partial appointments are impossible. |
| **Observability** | Structured JSON logs with a correlation identifier; counters for bookings attempted, confirmed and rejected by reason; latency histogram on the booking path; trace spans across handler, candidate selection and persistence. |
| **Maintainability** | Layered handler → service → repository. No business logic in handlers or SQL. Assignment policy behind an interface. |
| **Scalability** | Stateless service; horizontal scaling is safe because correctness is enforced in the database, not in application memory. |

---

## 12. Acceptance criteria

Test-ready statements. Identifiers are referenced from the test suite so that coverage of the specification is traceable.

**Happy path**

- `AC-01` A booking for an available slot returns 201 with an assigned technician and bay.
- `AC-02` The returned end time equals start plus the service type's duration.
- `AC-03` The returned customer is the owner of the supplied vehicle.

**Resource conflicts**

- `AC-04` When every qualifying bay is occupied for the requested interval, the request is rejected with `NO_AVAILABLE_RESOURCE` and `conflicting: ["BAY"]`.
- `AC-05` When every qualifying technician is occupied, the response indicates `["TECHNICIAN"]`.
- `AC-06` When both are occupied, both appear in `conflicting`.
- `AC-07` A vehicle with an overlapping active appointment is rejected with `VEHICLE_ALREADY_BOOKED`.

**Qualification**

- `AC-08` A technician who is free but lacks the required skill is not assigned; if no other qualifies, the booking is rejected.
- `AC-09` A bay that is free but of the wrong type is not assigned.

**Time boundaries**

- `AC-10` An appointment ending exactly when another begins is accepted — intervals are half-open.
- `AC-11` An appointment starting exactly when another ends is accepted.
- `AC-12` An appointment overlapping by one minute is rejected.
- `AC-13` A booking starting before opening time is rejected with `OUTSIDE_BUSINESS_HOURS`.
- `AC-14` A booking starting within hours but ending after closing is rejected with `SERVICE_EXCEEDS_CLOSING_TIME`.
- `AC-15` A booking on a day the dealership is closed is rejected.
- `AC-16` A booking in the past is rejected with `START_TIME_IN_PAST`.

**Cancellation semantics**

- `AC-17` A cancelled appointment does not block a new booking for the same resources and interval.

**Concurrency**

- `AC-18` Given exactly one qualifying bay and one qualifying technician, N concurrent identical requests produce exactly one 201 and N−1 rejections with `NO_AVAILABLE_RESOURCE`. No invariant is violated.

**Idempotency**

- `AC-19` Two requests carrying the same `Idempotency-Key` produce one appointment; the second returns the first result.

**Assignment policy**

- `AC-20` Given two qualifying technicians with unequal load, the less loaded one is assigned.
- `AC-21` Given equal load, assignment is by ascending identifier and is repeatable across runs.

**Availability**

- `AC-22` Availability excludes slots that would fail any invariant.
- `AC-23` Availability excludes slots whose duration would run past closing time.
- `AC-24` A fully booked day returns 200 with an empty slot array.

---

## 13. Traceability

| Original requirement | Functional requirements | Invariants | Acceptance criteria |
|---|---|---|---|
| R1 — Resource constrained booking | FR-2 | INV-7 | AC-01, AC-03 |
| R2 — Real-time availability check | FR-1, FR-2 | INV-1 to INV-6 | AC-04 to AC-18, AC-22 to AC-24 |
| R3 — Confirmed appointment record | FR-2, FR-3 | INV-7 | AC-01, AC-02, AC-19 |

---

## 14. Open questions

Deferred rather than resolved. Recorded so they are visibly known, not overlooked.

1. Should availability reserve a short-lived hold so an advisor discussing options with a customer does not lose the slot? A hold introduces expiry and cleanup, and is the natural next increment.
2. Should diagnostic work be modelled as a distinct service type with a fixed investigative duration, followed by a second booking for the repair? This is how workshops handle unknown faults in practice.
3. Should overbooking be permitted for short jobs, accepting some risk in exchange for utilisation? This is a commercial decision, not a technical one.
4. Should assignment consider technician–customer continuity, so a returning customer sees the same technician? It conflicts with pure load balancing and needs a business ruling.
