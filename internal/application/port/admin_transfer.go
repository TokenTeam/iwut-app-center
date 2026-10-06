package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

var ErrDeveloperLifecycleNotFound = errors.New("developer lifecycle not found")

type DeveloperLifecycle struct {
	AuthID          shared.AuthID
	AccountActive   bool
	DeveloperStatus shared.DeveloperStatus
}

func (value DeveloperLifecycle) Eligible() bool {
	return value.AuthID.IsValid() && value.AccountActive && value.DeveloperStatus == shared.DeveloperStatusApproved
}

type DeveloperLifecycleDirectory interface {
	GetFresh(context.Context, []shared.AuthID) ([]DeveloperLifecycle, error)
}

type ApplicationAdminTransferIDGenerator interface {
	NewUUIDv7() (domain.ApplicationAdminTransferID, error)
}

type ApplicationAdminTransferRepository interface {
	GetOwnership(context.Context, shared.ApplicationID, shared.AuthID, time.Time) (domain.ApplicationOwnership, error)
	Initiate(context.Context, shared.ApplicationID, shared.AuthID, shared.AuthID, int64, domain.ApplicationAdminTransferID, time.Time) (domain.ApplicationAdminTransfer, error)
	Get(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error)
	Accept(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, domain.ConfidentialCredentialHandling, time.Time) (domain.AcceptApplicationAdminTransferResult, error)
	Reject(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error)
	Cancel(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error)
}
