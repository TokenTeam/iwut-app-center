package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"strings"
)

type RevokeTesterJoinLinkHandler struct {
	clock      port.Clock
	repository port.ApplicationTesterRevocationRepository
}

func NewRevokeTesterJoinLinkHandler(clock port.Clock, repository port.ApplicationTesterRevocationRepository) *RevokeTesterJoinLinkHandler {
	return &RevokeTesterJoinLinkHandler{clock, repository}
}

func (h *RevokeTesterJoinLinkHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, appID shared.ApplicationID, joinLinkID domain.ApplicationTesterJoinLinkID) (*domain.RevokeTesterJoinLinkResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if !appID.IsValid() {
		return nil, domain.ErrInvalidApplicationId
	}
	appID = shared.ApplicationID(strings.ToLower(appID.String()))
	joinLinkID, err := domain.ParseTesterJoinLinkID(joinLinkID.String())
	if err != nil {
		return nil, err
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadRevocationCandidate(ctx, appID, joinLinkID, identity.AuthID)
	if err != nil {
		return nil, mapRevocationRepositoryError(err)
	}
	if candidate == nil {
		return nil, domain.ErrApplicationTesterJoinLinkStateInconsistent
	}
	link := candidate.JoinLink()
	if link == nil || link.ApplicationID() != appID || link.JoinLinkID() != joinLinkID {
		return nil, domain.ErrApplicationTesterJoinLinkStateInconsistent
	}
	if link.Status() == domain.JoinLinkStatusRevoked {
		return domain.NewRevokeTesterJoinLinkResult(link, false)
	}
	if h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.Revoke(ctx, appID, joinLinkID, identity.AuthID, at)
	if err != nil {
		return nil, mapRevocationRepositoryError(err)
	}
	if result == nil {
		return nil, domain.ErrApplicationTesterJoinLinkStateInconsistent
	}
	stored := result.JoinLink()
	if stored == nil || stored.ApplicationID() != appID || stored.JoinLinkID() != joinLinkID || stored.TokenHash() != link.TokenHash() || stored.CreatedBy() != link.CreatedBy() || !stored.CreatedAt().Equal(link.CreatedAt()) || stored.Status() != domain.JoinLinkStatusRevoked || stored.RevokedBy() == nil || stored.RevokedAt() == nil || stored.RevocationReason() == nil {
		return nil, domain.ErrApplicationTesterJoinLinkStateInconsistent
	}
	if result.Revoked() && (*stored.RevokedBy() != identity.AuthID || *stored.RevocationReason() != domain.RevocationReasonManual || stored.ReplacedByJoinLinkID() != nil) {
		return nil, domain.ErrApplicationTesterJoinLinkStateInconsistent
	}
	return result, nil
}

func mapRevocationRepositoryError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationTesterJoinLinkNotFound, domain.ErrApplicationTesterJoinLinkNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationTesterJoinLinkStateInconsistent, domain.ErrApplicationTesterJoinLinkStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	// Driver failures may contain indexed credentials. No arbitrary cause crosses
	// the capability boundary or becomes eligible for ordinary error logging.
	return domain.NewInternalError(nil)
}
