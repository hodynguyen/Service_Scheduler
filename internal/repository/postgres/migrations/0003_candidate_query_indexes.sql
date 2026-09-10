-- 0003_candidate_query_indexes.sql
--
-- SUPERSEDED BY 0004. The premise below is wrong: candidate selection does not
-- ask the database "which technicians hold skill X". It loads every technician
-- of the dealership with their skills and filters in domain.QualifiedTechnicians,
-- because qualification is a business rule and business rules stay out of SQL.
-- The SQL here is left exactly as applied — rewriting an applied migration would
-- leave already-migrated databases holding an index this file no longer creates.
-- 0004 drops it and adds the index the real query needs.
CREATE INDEX technician_skill_by_skill_idx
    ON technician_skill (skill_id, technician_id);
