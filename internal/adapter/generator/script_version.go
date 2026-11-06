package generator

import (
	"github.com/google/uuid"

	"iwut-app-center/internal/script/domain"
)

type ScriptVersionUUIDv7Generator struct{}

func NewScriptVersionUUIDv7Generator() *ScriptVersionUUIDv7Generator {
	return &ScriptVersionUUIDv7Generator{}
}

func (*ScriptVersionUUIDv7Generator) NewUUIDv7() (domain.VersionID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.VersionID(id.String()), nil
}
