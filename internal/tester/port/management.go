package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
)

var (
	ErrInvalidManagementPageToken  = errors.New("invalid tester management page token")
	ErrManagementNotFound          = errors.New("tester management not found")
	ErrManagementStateInconsistent = errors.New("tester management state inconsistent")
	ErrManagementUnavailable       = errors.New("tester management unavailable")
)

type ManagementQueryRepository interface {
	GetManagement(context.Context, shared.AuthID, shared.ApplicationID) (*domain.TesterManagement, error)
	ListMemberships(context.Context, shared.AuthID, shared.ApplicationID, domain.MembershipQueryStatus, int32, string) (*domain.TesterMembershipPage, error)
	ListJoinLinks(context.Context, shared.AuthID, shared.ApplicationID, domain.JoinLinkQueryStatus, int32, string) (*domain.TesterJoinLinkPage, error)
}
