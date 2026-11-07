package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/script/domain"
)

type ScriptPublicationUUIDv7Generator struct{}

func NewScriptPublicationUUIDv7Generator() *ScriptPublicationUUIDv7Generator {
	return &ScriptPublicationUUIDv7Generator{}
}

func (*ScriptPublicationUUIDv7Generator) NewUUIDv7() (domain.PublicationEventID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.PublicationEventID(id.String()), nil
}
