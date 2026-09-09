-- 0003_candidate_query_indexes.sql
-- Candidate selection asks "which technicians hold skill X"; the
-- technician_skill primary key leads with technician_id, so this
-- direction was unindexed.
CREATE INDEX technician_skill_by_skill_idx
    ON technician_skill (skill_id, technician_id);
