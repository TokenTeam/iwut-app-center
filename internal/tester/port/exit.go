package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"time"
)

var ErrApplicationTesterExitUnavailable = errors.New("application tester exit unavailable")

// Candidate and final mutation authorize the exact owned episode, validate all
// necessary sources, and serialize with existing writers through the App fence.
type ApplicationTesterExitRepository interface {
	LoadExitCandidate(context.Context, shared.ApplicationID, domain.ApplicationTesterMembershipID, shared.AuthID) (*domain.ApplicationTesterMembership, error)
	Leave(context.Context, shared.ApplicationID, domain.ApplicationTesterMembershipID, shared.AuthID, time.Time) (*domain.LeaveApplicationAsTesterResult, error)
}
