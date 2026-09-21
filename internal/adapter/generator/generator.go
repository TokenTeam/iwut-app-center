package generator

import (
	"time"

	"github.com/google/uuid"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

// UUIDv7Generator produces RFC 9562 version 7 identifiers through the
// Application use-case port.
type UUIDv7Generator struct{}

var _ port.ApplicationIDGenerator = (*UUIDv7Generator)(nil)

func NewUUIDv7Generator() *UUIDv7Generator {
	return &UUIDv7Generator{}
}

// ApplicationVersionUUIDv7Generator is separate because the two capability
// ports intentionally return different domain ID types.
type ApplicationVersionUUIDv7Generator struct{}

var _ versionport.ApplicationVersionIDGenerator = (*ApplicationVersionUUIDv7Generator)(nil)

func NewApplicationVersionUUIDv7Generator() *ApplicationVersionUUIDv7Generator {
	return &ApplicationVersionUUIDv7Generator{}
}

type ApplicationReviewUUIDv7Generator struct{}

var _ reviewport.ApplicationReviewIDGenerator = (*ApplicationReviewUUIDv7Generator)(nil)

func NewApplicationReviewUUIDv7Generator() *ApplicationReviewUUIDv7Generator {
	return &ApplicationReviewUUIDv7Generator{}
}

func (generator *ApplicationReviewUUIDv7Generator) NewUUIDv7() (reviewdomain.ApplicationReviewID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return reviewdomain.ApplicationReviewID(id.String()), nil
}

func (generator *ApplicationVersionUUIDv7Generator) NewUUIDv7() (versiondomain.ApplicationVersionID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return versiondomain.ApplicationVersionID(id.String()), nil
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
var _ versionport.Clock = (*SystemClock)(nil)
var _ reviewport.Clock = (*SystemClock)(nil)

func NewSystemClock() *SystemClock {
	return &SystemClock{}
}

func (clock *SystemClock) Now() time.Time {
	return time.Now().UTC()
}
