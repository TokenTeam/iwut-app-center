package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"time"
)

var ErrApplicationTesterJoinLinkStateInconsistent = errors.New("application tester join link state is inconsistent")

// Both methods lock the Application write fence before checking its current
// administrator and the exact link. A revoked candidate is final; an active
// candidate is only preliminary. Neither method reads or changes memberships.
type ApplicationTesterRevocationRepository interface {
	LoadRevocationCandidate(context.Context, shared.ApplicationID, domain.ApplicationTesterJoinLinkID, shared.AuthID) (*domain.TesterJoinLinkRevocationCandidate, error)
	Revoke(context.Context, shared.ApplicationID, domain.ApplicationTesterJoinLinkID, shared.AuthID, time.Time) (*domain.RevokeTesterJoinLinkResult, error)
}
