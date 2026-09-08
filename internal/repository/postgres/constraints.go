package postgres

// Names of the exclusion constraints declared in the schema. The repository
// maps SQLSTATE 23P01 (exclusion_violation) on these names to domain errors.
const (
	ConstraintBayOverlap        = "appointment_no_bay_overlap"
	ConstraintTechnicianOverlap = "appointment_no_technician_overlap"
	ConstraintVehicleOverlap    = "appointment_no_vehicle_overlap"
)
