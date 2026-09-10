package service_test

import (
	"fmt"

	"github.com/hodynguyen/service-scheduler/internal/service"
)

// fmtLostRace mirrors how the repository wraps a database rejection so the
// service sees exactly the error shape it does in production.
func fmtLostRace(err error) error {
	return fmt.Errorf("%w: %w", service.ErrLostRace, err)
}
