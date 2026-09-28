package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/oauthclient/domain"
)

type OAuthClientUUIDv4Generator struct{}

func NewOAuthClientUUIDv4Generator() *OAuthClientUUIDv4Generator {
	return &OAuthClientUUIDv4Generator{}
}
func (*OAuthClientUUIDv4Generator) NewUUIDv4() (domain.ClientID, error) {
	return domain.ParseClientID(uuid.NewString())
}
