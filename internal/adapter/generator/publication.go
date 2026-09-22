package generator

import (
	"github.com/google/uuid"
	publicationport "iwut-app-center/internal/publication/port"
)

type PublicationUUIDv7Generator struct{}

func NewPublicationUUIDv7Generator() *PublicationUUIDv7Generator {
	return &PublicationUUIDv7Generator{}
}
func (g *PublicationUUIDv7Generator) NewUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

var _ publicationport.UUIDv7Generator = (*PublicationUUIDv7Generator)(nil)
var _ publicationport.Clock = (*SystemClock)(nil)
