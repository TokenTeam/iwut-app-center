package generator

import (
	"github.com/google/uuid"

	"iwut-app-center/internal/script/domain"
)

type ScriptResourceUUIDv7Generator struct{}

func NewScriptResourceUUIDv7Generator() *ScriptResourceUUIDv7Generator {
	return &ScriptResourceUUIDv7Generator{}
}

func (*ScriptResourceUUIDv7Generator) NewUUIDv7() (domain.ResourceID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ResourceID(id.String()), nil
}
