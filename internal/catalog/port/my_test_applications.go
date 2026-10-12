package port

import (
	"context"
	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/shared"
)

// MyTestApplicationRepository composes each page/detail inside one read-only
// snapshot; cursors carry position and binding, never participation authority.
type MyTestApplicationRepository interface {
	ListMyTest(context.Context, shared.AuthID, int32, []domain.CapabilityName, int32, string) (*domain.MyTestApplicationPage, error)
	GetMyTest(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.MyTestApplicationDetail, error)
}
