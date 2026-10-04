package generator

import (
	"github.com/google/uuid"
	filterdomain "iwut-app-center/internal/filter/domain"
)

type ApplicationFilterRevisionUUIDv7Generator struct{}

func NewApplicationFilterRevisionUUIDv7Generator() *ApplicationFilterRevisionUUIDv7Generator {
	return &ApplicationFilterRevisionUUIDv7Generator{}
}
func (*ApplicationFilterRevisionUUIDv7Generator) NewUUIDv7() (filterdomain.FilterRevisionID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return filterdomain.FilterRevisionID(id.String()), nil
}
