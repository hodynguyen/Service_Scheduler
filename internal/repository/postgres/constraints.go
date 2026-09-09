package postgres

// Names of the exclusion constraints declared in the schema. The repository
// maps SQLSTATE 23P01 (exclusion_violation) on these names to domain errors.
const (
	ConstraintBayOverlap        = "appointment_no_bay_overlap"
	ConstraintTechnicianOverlap = "appointment_no_technician_overlap"
	ConstraintVehicleOverlap    = "appointment_no_vehicle_overlap"
)

// Foreign keys from migration 0002 that enforce INV-4 / INV-5 in the database.
const (
	ConstraintRequiredSkillMatchesServiceType   = "appointment_required_skill_matches_service_type"
	ConstraintRequiredBayTypeMatchesServiceType = "appointment_required_bay_type_matches_service_type"
	ConstraintTechnicianHoldsSkill              = "appointment_technician_holds_required_skill"
	ConstraintBayHasRequiredType                = "appointment_bay_has_required_type"
)
