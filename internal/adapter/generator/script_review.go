package generator

import (
	"github.com/google/uuid"

	"iwut-app-center/internal/script/domain"
)

type ScriptReviewUUIDv7Generator struct{}

func NewScriptReviewUUIDv7Generator() *ScriptReviewUUIDv7Generator {
	return &ScriptReviewUUIDv7Generator{}
}

func (*ScriptReviewUUIDv7Generator) NewUUIDv7() (domain.ReviewID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ReviewID(id.String()), nil
}
