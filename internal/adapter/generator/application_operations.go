package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/application/domain"
)

type ApplicationOperationUUIDv7Generator struct{}

func NewApplicationOperationUUIDv7Generator() *ApplicationOperationUUIDv7Generator {
	return &ApplicationOperationUUIDv7Generator{}
}
func (*ApplicationOperationUUIDv7Generator) NewUUIDv7() (domain.ApplicationOperationEventID, error) {
	id, err := uuid.NewV7()
	return domain.ApplicationOperationEventID(id.String()), err
}
