package usecase

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type IncomingApplicationAdminTransfers struct {
	repository port.IncomingApplicationAdminTransferRepository
	clock      port.Clock
}

func NewIncomingApplicationAdminTransfers(repository port.IncomingApplicationAdminTransferRepository, clock port.Clock) *IncomingApplicationAdminTransfers {
	return &IncomingApplicationAdminTransfers{repository, clock}
}
func (h *IncomingApplicationAdminTransfers) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, size int32, token string) (domain.IncomingApplicationAdminTransferPage, error) {
	var empty domain.IncomingApplicationAdminTransferPage
	if !identity.AuthID.IsValid() {
		return empty, domain.ErrIncomingTransferIdentityRequired
	}
	query, err := domain.NewIncomingTransferQuery(size, token)
	if err != nil {
		return empty, err
	}
	at := h.clock.Now().UTC().Truncate(time.Millisecond)
	page, err := h.repository.ListIncoming(ctx, identity.AuthID, query, at)
	if err != nil {
		for _, known := range []error{domain.ErrInvalidIncomingTransferPageToken, domain.ErrApplicationAdminTransferStateInconsistent, domain.ErrIncomingTransferUnavailable} {
			if errors.Is(err, known) {
				return empty, known
			}
		}
		return empty, domain.ErrIncomingTransferUnavailable
	}
	if err = page.Validate(identity.AuthID, query, at); err != nil {
		return empty, err
	}
	return page, nil
}
