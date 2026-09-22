package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"time"
)

var (
	ErrApplicationTesterMembershipNotFound = errors.New("application tester membership not found")
	ErrApplicationTesterStateInconsistent  = errors.New("application tester state is inconsistent")
)

// ApplicationTesterRemovalRepository owns the administrator and exact episode
// checks. Both operations use the shared Application write fence; a REMOVED
// candidate is already an authorized idempotent snapshot and needs no Clock.
type ApplicationTesterRemovalRepository interface {
	LoadRemovalCandidate(context.Context, shared.ApplicationID, domain.ApplicationTesterMembershipID, shared.AuthID) (*domain.TesterRemovalCandidate, error)
	Remove(context.Context, shared.ApplicationID, domain.ApplicationTesterMembershipID, shared.AuthID, time.Time) (*domain.RemoveApplicationTesterResult, error)
}
