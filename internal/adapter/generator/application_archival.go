package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/application/domain"
)

type ApplicationArchiveUUIDv7Generator struct{}

func NewApplicationArchiveUUIDv7Generator() *ApplicationArchiveUUIDv7Generator {
	return &ApplicationArchiveUUIDv7Generator{}
}

func (*ApplicationArchiveUUIDv7Generator) NewUUIDv7() (domain.ApplicationArchiveEventID, error) {
	id, err := uuid.NewV7()
	return domain.ApplicationArchiveEventID(id.String()), err
}
