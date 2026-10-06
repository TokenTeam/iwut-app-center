package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/application/domain"
)

type ApplicationClosureUUIDv7Generator struct{}

func NewApplicationClosureUUIDv7Generator() *ApplicationClosureUUIDv7Generator {
	return &ApplicationClosureUUIDv7Generator{}
}
func (*ApplicationClosureUUIDv7Generator) NewUUIDv7() (domain.ApplicationClosureID, error) {
	id, err := uuid.NewV7()
	return domain.ApplicationClosureID(id.String()), err
}
