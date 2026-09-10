-- 0004_technician_by_dealership_idx.sql
--
-- Replaces the index added by 0003, which was created for a query that does not
-- exist. What actually runs on every availability request, and once per booking
-- attempt (repository.go, daySchedule):
--
--   SELECT t.id, t.name, array_agg(ts.skill_id) ...
--   FROM technician t LEFT JOIN technician_skill ts ON ts.technician_id = t.id
--   WHERE t.dealership_id = $1
--   GROUP BY t.id, t.name ORDER BY t.id;
--
-- technician had only PRIMARY KEY (id) and UNIQUE (id, dealership_id); neither
-- leads with dealership_id, so the predicate had no seek path. The sibling bay
-- query is covered by service_bay's UNIQUE (dealership_id, name) — the gap was
-- an accident of which unique constraints happened to exist. (dealership_id, id)
-- serves both the predicate and the ORDER BY.
--
-- Honest caveat: with the seeded dealership (4 technicians) the planner chooses
-- a sequential scan and ignores this index, correctly. It exists so the claim in
-- requirements.md §11 that availability queries are index-supported holds at a
-- realistic number of technicians, not so it changes the seed-data plan.
--
-- The 0003 index is dropped rather than kept: technician_skill's primary key
-- (technician_id, skill_id) already serves every lookup the code and the
-- INV-4 foreign key perform, so the second index only adds write cost.

CREATE INDEX technician_by_dealership_idx ON technician (dealership_id, id);

DROP INDEX IF EXISTS technician_skill_by_skill_idx;
