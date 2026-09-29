package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

type DeveloperIdentity = shared.DeveloperIdentity
type DeveloperStatus = shared.DeveloperStatus

const DeveloperStatusApproved = shared.DeveloperStatusApproved

type SubmitApplicationVersionReviewCommand struct {
	ExpectedRevision int64
}

type SubmitApplicationVersionReviewHandler struct {
	scopeCatalog port.ScopeCatalog
	launchPolicy port.LaunchURLSubmissionPolicy
	oauthPolicy  port.OAuthRedirectPolicy
	idGenerator  port.ApplicationReviewIDGenerator
	clock        port.Clock
	repository   port.ApplicationReviewRepository
}

func NewSubmitApplicationVersionReviewHandler(
	scopeCatalog port.ScopeCatalog,
	launchPolicy port.LaunchURLSubmissionPolicy,
	oauthPolicy port.OAuthRedirectPolicy,
	idGenerator port.ApplicationReviewIDGenerator,
	clock port.Clock,
	repository port.ApplicationReviewRepository,
) *SubmitApplicationVersionReviewHandler {
	return &SubmitApplicationVersionReviewHandler{
		scopeCatalog: scopeCatalog,
		launchPolicy: launchPolicy,
		oauthPolicy:  oauthPolicy,
		idGenerator:  idGenerator,
		clock:        clock,
		repository:   repository,
	}
}

func (handler *SubmitApplicationVersionReviewHandler) Handle(
	ctx context.Context,
	identity DeveloperIdentity,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	command SubmitApplicationVersionReviewCommand,
) (*domain.ReviewSubmissionResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if command.ExpectedRevision < 1 {
		return nil, domain.ErrApplicationVersionRevisionRequired
	}
	if !applicationID.IsValid() || !versionID.IsValid() {
		return nil, domain.ErrApplicationVersionNotFound
	}
	if handler == nil || handler.scopeCatalog == nil || handler.launchPolicy == nil || handler.oauthPolicy == nil ||
		handler.idGenerator == nil || handler.clock == nil || handler.repository == nil {
		return nil, domain.NewInternalError(nil)
	}

	candidate, err := handler.repository.LoadSubmissionCandidate(
		ctx, applicationID, versionID, identity.AuthID, command.ExpectedRevision,
	)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil || candidate.ApplicationID() != applicationID || candidate.VersionID() != versionID ||
		candidate.Revision() != command.ExpectedRevision {
		return nil, domain.NewInternalError(nil)
	}
	redirects := candidate.Snapshot().OAuthRedirects()
	if err := handler.oauthPolicy.Validate(redirects.PKCERedirectURIs(), redirects.ConfidentialRedirectURIs()); err != nil {
		if errors.Is(err, port.ErrOAuthRedirectNotReviewable) {
			return nil, domain.ErrInvalidOAuthRedirectConfiguration
		}
		return nil, domain.NewInternalError(err)
	}

	scopeCatalogRevision, err := handler.scopeCatalog.EnsureAllRequestable(ctx, candidate.AllScopes())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrScopeNotRequestable):
			return nil, domain.ErrInvalidApplicationScope
		case errors.Is(err, port.ErrScopeCatalogUnavailable):
			return nil, domain.NewScopeCatalogUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if scopeCatalogRevision < 1 {
		return nil, domain.NewInternalError(nil)
	}

	snapshot := candidate.Snapshot()
	preflightPolicyVersion, err := handler.launchPolicy.Inspect(ctx, snapshot.LaunchURL())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrLaunchURLNotReviewable):
			return nil, domain.ErrApplicationLaunchURLNotReviewable
		case errors.Is(err, port.ErrLaunchURLInspectionUnavailable):
			return nil, domain.NewLaunchURLInspectionUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if _, err := domain.NewPreflightPolicyVersion(preflightPolicyVersion.String()); err != nil {
		return nil, domain.NewInternalError(err)
	}

	reviewID, err := handler.idGenerator.NewUUIDv7()
	if err != nil || !reviewID.IsValid() {
		return nil, domain.NewInternalError(err)
	}
	submittedAt := handler.clock.Now().UTC()
	if submittedAt.IsZero() {
		return nil, domain.NewInternalError(nil)
	}

	result, err := handler.repository.Submit(
		ctx, candidate, reviewID, identity.AuthID, scopeCatalogRevision, preflightPolicyVersion, submittedAt,
	)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

func mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, port.ErrApplicationVersionNotFound):
		return domain.ErrApplicationVersionNotFound
	case errors.Is(err, port.ErrApplicationAdminRequired):
		return domain.ErrApplicationAdminRequired
	case errors.Is(err, port.ErrApplicationVersionNotDraft):
		return domain.ErrApplicationVersionNotDraft
	case errors.Is(err, port.ErrApplicationVersionRevisionConflict):
		return domain.ErrApplicationVersionRevisionConflict
	default:
		return domain.NewInternalError(err)
	}
}
