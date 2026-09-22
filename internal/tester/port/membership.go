package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
)

var (
	ErrTesterJoinLinkInvalid         = errors.New("tester join link is invalid")
	ErrApplicationTesterLimitReached = errors.New("application tester limit reached")
)

type TesterJoinTokenHasher interface{ Hash([32]byte) [32]byte }
type ApplicationTesterMembershipRepository interface {
	ResolveJoinCandidate(context.Context, domain.ApplicationTesterJoinLinkID, [32]byte) (*domain.TesterJoinCandidate, error)
	Join(context.Context, domain.ApplicationTesterJoinLinkID, [32]byte, shared.AuthID, *domain.ApplicationTesterMembership, int32) (*domain.JoinApplicationAsTesterResult, error)
}
