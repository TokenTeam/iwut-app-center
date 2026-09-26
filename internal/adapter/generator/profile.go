package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
)

type ApplicationProfileRevisionUUIDv7Generator struct{}

var _ port.ApplicationProfileRevisionIDGenerator = (*ApplicationProfileRevisionUUIDv7Generator)(nil)

func NewApplicationProfileRevisionUUIDv7Generator() *ApplicationProfileRevisionUUIDv7Generator {
	return &ApplicationProfileRevisionUUIDv7Generator{}
}
func (g *ApplicationProfileRevisionUUIDv7Generator) NewUUIDv7() (domain.ApplicationProfileRevisionID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ApplicationProfileRevisionID(id.String()), nil
}

type ApplicationProfileReviewUUIDv7Generator struct{}

func NewApplicationProfileReviewUUIDv7Generator() *ApplicationProfileReviewUUIDv7Generator {
	return &ApplicationProfileReviewUUIDv7Generator{}
}
func (g *ApplicationProfileReviewUUIDv7Generator) NewUUIDv7() (domain.ApplicationProfileReviewID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ApplicationProfileReviewID(id.String()), nil
}
