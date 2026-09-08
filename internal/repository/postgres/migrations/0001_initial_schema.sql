-- 0001_initial_schema.sql
-- Entities from docs/requirements.md §4 plus the tables needed for FR-4.
-- All instants are timestamptz (UTC). Intervals are half-open [start, end).

CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TYPE bay_type AS ENUM ('GENERAL', 'ALIGNMENT', 'EV');
CREATE TYPE appointment_status AS ENUM ('CONFIRMED', 'CANCELLED');

CREATE TABLE dealership (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    timezone   text        NOT NULL,                -- IANA name, e.g. Asia/Ho_Chi_Minh (A-9)
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Weekly opening hours. weekday: 0 = Sunday .. 6 = Saturday (matches Go's
-- time.Weekday and Postgres EXTRACT(DOW)). No row for a weekday = closed.
CREATE TABLE business_hours (
    dealership_id uuid     NOT NULL REFERENCES dealership(id) ON DELETE CASCADE,
    weekday       smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    opens_at      time     NOT NULL,
    closes_at     time     NOT NULL,
    PRIMARY KEY (dealership_id, weekday),
    CONSTRAINT business_hours_open_before_close CHECK (opens_at < closes_at)
);

CREATE TABLE skill (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,                       -- e.g. EV_HIGH_VOLTAGE
    name text NOT NULL
);

CREATE TABLE service_bay (
    id            uuid     PRIMARY KEY DEFAULT gen_random_uuid(),
    dealership_id uuid     NOT NULL REFERENCES dealership(id),
    name          text     NOT NULL,
    bay_type      bay_type NOT NULL,
    UNIQUE (dealership_id, name),
    UNIQUE (id, dealership_id)                       -- target of the INV-7 composite FK
);

CREATE TABLE technician (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dealership_id uuid NOT NULL REFERENCES dealership(id),
    name          text NOT NULL,
    UNIQUE (id, dealership_id)                       -- target of the INV-7 composite FK
);

CREATE TABLE technician_skill (
    technician_id uuid NOT NULL REFERENCES technician(id) ON DELETE CASCADE,
    skill_id      uuid NOT NULL REFERENCES skill(id),
    PRIMARY KEY (technician_id, skill_id)
);

-- Catalogue entry (A-1, A-2, A-3): duration, one required skill, one required bay type.
CREATE TABLE service_type (
    id                uuid     PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text     NOT NULL UNIQUE,
    duration_minutes  integer  NOT NULL CHECK (duration_minutes > 0),
    required_skill_id uuid     NOT NULL REFERENCES skill(id),
    required_bay_type bay_type NOT NULL
);

CREATE TABLE customer (
    id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name  text NOT NULL,
    email text,
    phone text
);

-- A vehicle is registered with one dealership (BR-7) and owned by one customer (A-6).
CREATE TABLE vehicle (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dealership_id uuid NOT NULL REFERENCES dealership(id),
    customer_id   uuid NOT NULL REFERENCES customer(id),
    vin           text NOT NULL UNIQUE,
    model         text NOT NULL,
    UNIQUE (id, dealership_id)                       -- target of the INV-7 composite FK
);

CREATE TABLE appointment (
    id              uuid               PRIMARY KEY DEFAULT gen_random_uuid(),
    dealership_id   uuid               NOT NULL REFERENCES dealership(id),
    vehicle_id      uuid               NOT NULL,
    customer_id     uuid               NOT NULL REFERENCES customer(id),
    service_type_id uuid               NOT NULL REFERENCES service_type(id),
    technician_id   uuid               NOT NULL,
    bay_id          uuid               NOT NULL,
    start_time      timestamptz        NOT NULL,
    end_time        timestamptz        NOT NULL,
    status          appointment_status NOT NULL DEFAULT 'CONFIRMED',
    created_at      timestamptz        NOT NULL DEFAULT now(),

    CONSTRAINT appointment_start_before_end CHECK (start_time < end_time),

    -- INV-7: technician, bay and vehicle must belong to the appointment's dealership.
    CONSTRAINT appointment_vehicle_same_dealership
        FOREIGN KEY (vehicle_id, dealership_id) REFERENCES vehicle(id, dealership_id),
    CONSTRAINT appointment_technician_same_dealership
        FOREIGN KEY (technician_id, dealership_id) REFERENCES technician(id, dealership_id),
    CONSTRAINT appointment_bay_same_dealership
        FOREIGN KEY (bay_id, dealership_id) REFERENCES service_bay(id, dealership_id),

    -- INV-1..INV-3: the correctness guarantee. Half-open ranges, CONFIRMED only (BR-9).
    CONSTRAINT appointment_no_bay_overlap
        EXCLUDE USING gist (bay_id WITH =, tstzrange(start_time, end_time, '[)') WITH &&)
        WHERE (status = 'CONFIRMED'),
    CONSTRAINT appointment_no_technician_overlap
        EXCLUDE USING gist (technician_id WITH =, tstzrange(start_time, end_time, '[)') WITH &&)
        WHERE (status = 'CONFIRMED'),
    CONSTRAINT appointment_no_vehicle_overlap
        EXCLUDE USING gist (vehicle_id WITH =, tstzrange(start_time, end_time, '[)') WITH &&)
        WHERE (status = 'CONFIRMED')
);

-- Availability and candidate queries read one dealership's confirmed appointments for a day.
CREATE INDEX appointment_dealership_day_idx
    ON appointment (dealership_id, start_time)
    WHERE status = 'CONFIRMED';

-- FR-4: idempotency keys scoped per dealership, retained for 24 hours.
CREATE TABLE idempotency_key (
    dealership_id       uuid        NOT NULL REFERENCES dealership(id),
    key                 uuid        NOT NULL,
    request_fingerprint text        NOT NULL,
    outcome             text        NOT NULL CHECK (outcome IN ('CREATED', 'REJECTED')),
    appointment_id      uuid        REFERENCES appointment(id),
    error_code          text,
    error_conflicting   text[],
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    PRIMARY KEY (dealership_id, key),
    CONSTRAINT idempotency_outcome_shape CHECK (
        (outcome = 'CREATED'  AND appointment_id IS NOT NULL AND error_code IS NULL) OR
        (outcome = 'REJECTED' AND appointment_id IS NULL     AND error_code IS NOT NULL)
    )
);

CREATE INDEX idempotency_key_expires_idx ON idempotency_key (expires_at);
