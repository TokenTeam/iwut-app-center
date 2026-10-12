package port

import (
	"context"
	"time"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

// TestClearAuditFactory is invoked only after the transaction proves an actual
// change. The usecase memoizes its result across transaction retries.
type TestClearAuditFactory func() (domain.ApplicationPublicationHistoryID, time.Time, error)

type TestPublicationClearRepository interface {
	ClearTest(context.Context, shared.ApplicationID, int32, shared.AuthID, int64, TestClearAuditFactory) (*domain.PlaceInTestResult, error)
}
