-- 0002_enforce_skill_and_bay_type.sql
-- INV-4 (technician holds the required skill) and INV-5 (bay has the required
-- type) were enforced only by the domain candidate filter. This migration makes
-- the database reject violations too, using only foreign keys:
--
--   appointment.required_skill_id / required_bay_type are copied from the
--   service type at insert time and pinned to it by composite FKs, so a writer
--   cannot lie about them; two further composite FKs require the chosen
--   technician to hold that skill and the chosen bay to have that type.
--
-- Consequence: changing a service type's requirement, decertifying a
-- technician, or retyping a bay is blocked while appointments reference the
-- old value (RESTRICT). That is intended for CONFIRMED rows; historical rows
-- would need a data migration first.

ALTER TABLE service_type
    ADD CONSTRAINT service_type_id_required_skill_unique    UNIQUE (id, required_skill_id),
    ADD CONSTRAINT service_type_id_required_bay_type_unique UNIQUE (id, required_bay_type);

ALTER TABLE service_bay
    ADD CONSTRAINT service_bay_id_type_unique UNIQUE (id, bay_type);

ALTER TABLE appointment
    ADD COLUMN required_skill_id uuid,
    ADD COLUMN required_bay_type bay_type;

UPDATE appointment a
SET    required_skill_id = st.required_skill_id,
       required_bay_type = st.required_bay_type
FROM   service_type st
WHERE  st.id = a.service_type_id;

ALTER TABLE appointment
    ALTER COLUMN required_skill_id SET NOT NULL,
    ALTER COLUMN required_bay_type SET NOT NULL,
    -- the denormalised requirement must be the service type's requirement
    ADD CONSTRAINT appointment_required_skill_matches_service_type
        FOREIGN KEY (service_type_id, required_skill_id) REFERENCES service_type(id, required_skill_id),
    ADD CONSTRAINT appointment_required_bay_type_matches_service_type
        FOREIGN KEY (service_type_id, required_bay_type) REFERENCES service_type(id, required_bay_type),
    -- INV-4: the technician holds that skill
    ADD CONSTRAINT appointment_technician_holds_required_skill
        FOREIGN KEY (technician_id, required_skill_id) REFERENCES technician_skill(technician_id, skill_id),
    -- INV-5: the bay has that type
    ADD CONSTRAINT appointment_bay_has_required_type
        FOREIGN KEY (bay_id, required_bay_type) REFERENCES service_bay(id, bay_type);
