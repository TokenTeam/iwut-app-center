package generator

import (
	"time"

	"github.com/google/uuid"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
)

// UUIDv7Generator produces RFC 9562 version 7 identifiers through the
// Application use-case port.
type UUIDv7Generator struct{}

var _ port.ApplicationIDGenerator = (*UUIDv7Generator)(nil)

func NewUUIDv7Generator() *UUIDv7Generator {
	return &UUIDv7Generator{}
}

func (generator *UUIDv7Generator) NewUUIDv7() (domain.ApplicationID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ApplicationID(id.String()), nil
}

// SystemClock is the production time source. Domain and UseCase still receive
// time only through the Clock port.
type SystemClock struct{}

var _ port.Clock = (*SystemClock)(nil)

func NewSystemClock() *SystemClock {
	return &SystemClock{}
}

func (clock *SystemClock) Now() time.Time {
	return time.Now().UTC()
}
