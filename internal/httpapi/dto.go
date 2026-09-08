package httpapi

import (
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// createAppointmentRequest is the §10.2 request body. Duration, technician,
// bay and customer are deliberately absent (BR-8, FR-2, A-6).
type createAppointmentRequest struct {
	DealershipID  string `json:"dealershipId"`
	VehicleID     string `json:"vehicleId"`
	ServiceTypeID string `json:"serviceTypeId"`
	StartTime     string `json:"startTime"`
}

type refDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type vehicleDTO struct {
	ID    string `json:"id"`
	VIN   string `json:"vin"`
	Model string `json:"model"`
}

type bayDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BayType string `json:"bayType"`
}

// appointmentResponse is the §10.2 / §10.3 body.
type appointmentResponse struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	StartTime   string     `json:"startTime"`
	EndTime     string     `json:"endTime"`
	Customer    refDTO     `json:"customer"`
	Vehicle     vehicleDTO `json:"vehicle"`
	ServiceType refDTO     `json:"serviceType"`
	Technician  refDTO     `json:"technician"`
	Bay         bayDTO     `json:"bay"`
}

func toAppointmentResponse(a domain.Appointment) appointmentResponse {
	return appointmentResponse{
		ID:          a.ID,
		Status:      string(a.Status),
		StartTime:   formatTime(a.Interval.Start),
		EndTime:     formatTime(a.Interval.End),
		Customer:    refDTO{ID: a.Customer.ID, Name: a.Customer.Name},
		Vehicle:     vehicleDTO{ID: a.Vehicle.ID, VIN: a.Vehicle.VIN, Model: a.Vehicle.Model},
		ServiceType: refDTO{ID: a.ServiceType.ID, Name: a.ServiceType.Name},
		Technician:  refDTO{ID: a.Technician.ID, Name: a.Technician.Name},
		Bay:         bayDTO{ID: a.Bay.ID, Name: a.Bay.Name, BayType: string(a.Bay.Type)},
	}
}

type serviceTypeDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"durationMinutes"`
}

// availabilityResponse is the §10.1 body.
type availabilityResponse struct {
	Date           string         `json:"date"`
	ServiceType    serviceTypeDTO `json:"serviceType"`
	AvailableSlots []string       `json:"availableSlots"`
}

func toAvailabilityResponse(res service.AvailabilityResult) availabilityResponse {
	slots := make([]string, 0, len(res.Slots))
	for _, s := range res.Slots {
		slots = append(slots, formatTime(s))
	}
	return availabilityResponse{
		Date: res.Date.String(),
		ServiceType: serviceTypeDTO{
			ID:              res.ServiceType.ID,
			Name:            res.ServiceType.Name,
			DurationMinutes: int(res.ServiceType.Duration / time.Minute),
		},
		AvailableSlots: slots,
	}
}

// formatTime renders an instant as ISO-8601 with the explicit offset of the
// time's location — the dealership's, as set by the repository (A-9).
func formatTime(t time.Time) string { return t.Format(time.RFC3339) }
