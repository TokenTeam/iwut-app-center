package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
	"time"
)

type ManagementActivityQueries struct {
	repository port.ManagementActivityRepository
	clock      port.Clock
}

func NewManagementActivityQueries(r port.ManagementActivityRepository, c port.Clock) *ManagementActivityQueries {
	return &ManagementActivityQueries{r, c}
}
func (h *ManagementActivityQueries) List(ctx context.Context, caller shared.TrustedIdentity, rawApp string, input domain.ManagementActivityQuery) (domain.ManagementActivityPage, error) {
	var empty domain.ManagementActivityPage
	if !caller.AuthID.IsValid() {
		return empty, domain.ErrUserIdentityRequired
	}
	app, ok := shared.ParseApplicationID(rawApp)
	q, err := domain.NewManagementActivityQuery(input)
	if !ok || err != nil {
		return empty, domain.ErrInvalidManagementActivityQuery
	}
	if h == nil || h.repository == nil || h.clock == nil {
		return empty, domain.ErrManagementActivityUnavailable
	}
	at := h.clock.Now().UTC().Truncate(time.Millisecond)
	p, err := h.repository.ListManagementActivity(ctx, caller.AuthID, app, q, at)
	if err != nil {
		return empty, managementActivityError(err)
	}
	if err = p.Validate(app, q, at); err != nil {
		return empty, err
	}
	return p, nil
}
func (h *ManagementActivityQueries) Get(ctx context.Context, caller shared.TrustedIdentity, rawApp, rawID string) (domain.ManagementActivityDetail, error) {
	var empty domain.ManagementActivityDetail
	if !caller.AuthID.IsValid() {
		return empty, domain.ErrUserIdentityRequired
	}
	app, ok := shared.ParseApplicationID(rawApp)
	id, err := domain.ParseManagementActivityID(rawID)
	if !ok || err != nil {
		return empty, domain.ErrInvalidManagementActivityQuery
	}
	if h == nil || h.repository == nil || h.clock == nil {
		return empty, domain.ErrManagementActivityUnavailable
	}
	at := h.clock.Now().UTC().Truncate(time.Millisecond)
	d, err := h.repository.GetManagementActivity(ctx, caller.AuthID, app, id, at)
	if err != nil {
		return empty, managementActivityError(err)
	}
	if err = d.Validate(app, id, at); err != nil {
		return empty, err
	}
	return d, nil
}
func managementActivityError(err error) error {
	for _, expected := range []error{domain.ErrInvalidManagementActivityPageToken, domain.ErrManagementActivityNotFound, domain.ErrManagementActivityStateInconsistent, domain.ErrManagementActivityResponseTooLarge, domain.ErrManagementActivityUnavailable} {
		if errors.Is(err, expected) {
			return expected
		}
	}
	return domain.ErrManagementActivityUnavailable
}
