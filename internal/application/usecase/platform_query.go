package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
	"time"
)

const PermissionApplicationSearch = "app.application.search"

type PlatformApplicationQueries struct {
	repository port.PlatformApplicationQueryRepository
	clock      port.Clock
}

func NewPlatformApplicationQueries(repository port.PlatformApplicationQueryRepository, clock port.Clock) *PlatformApplicationQueries {
	return &PlatformApplicationQueries{repository, clock}
}

func AuthorizePlatformApplicationQuery(identity shared.TrustedIdentity) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrUserIdentityRequired
	}
	if !hasPermission(identity, PermissionApplicationSearch) {
		return domain.ErrApplicationPlatformSearchRequired
	}
	return nil
}

func (h *PlatformApplicationQueries) Search(ctx context.Context, identity shared.TrustedIdentity, input domain.PlatformApplicationQuery) (domain.PlatformApplicationPage, error) {
	var empty domain.PlatformApplicationPage
	if err := AuthorizePlatformApplicationQuery(identity); err != nil {
		return empty, err
	}
	q, err := domain.NewPlatformApplicationQuery(input)
	if err != nil {
		return empty, err
	}
	if h == nil || h.repository == nil || h.clock == nil {
		return empty, domain.ErrPlatformApplicationUnavailable
	}
	at := h.clock.Now().UTC().Truncate(time.Millisecond)
	page, err := h.repository.SearchPlatformApplications(ctx, identity.AuthID, q, at)
	if err != nil {
		return empty, platformQueryError(err)
	}
	if err = page.Validate(q, at); err != nil {
		return empty, err
	}
	return page, nil
}
func (h *PlatformApplicationQueries) Get(ctx context.Context, identity shared.TrustedIdentity, raw string) (domain.PlatformApplicationSummary, error) {
	var empty domain.PlatformApplicationSummary
	if err := AuthorizePlatformApplicationQuery(identity); err != nil {
		return empty, err
	}
	id, ok := shared.ParseApplicationID(raw)
	if !ok {
		return empty, domain.ErrInvalidPlatformApplicationQuery
	}
	if h == nil || h.repository == nil {
		return empty, domain.ErrPlatformApplicationUnavailable
	}
	s, err := h.repository.GetPlatformApplicationSummary(ctx, id)
	if err != nil {
		return empty, platformQueryError(err)
	}
	if s.Validate() != nil || s.ApplicationID != id {
		return empty, domain.ErrPlatformApplicationStateInconsistent
	}
	return s, nil
}
func platformQueryError(err error) error {
	for _, known := range []error{domain.ErrInvalidPlatformApplicationPageToken, domain.ErrPlatformApplicationNotFound, domain.ErrPlatformApplicationStateInconsistent, domain.ErrPlatformApplicationUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return domain.ErrPlatformApplicationUnavailable
}
