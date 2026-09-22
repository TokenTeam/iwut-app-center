package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"strings"
)

type CreateOrRotateTesterJoinLinkCommand struct {
	ExpectedActiveJoinLinkID *domain.ApplicationTesterJoinLinkID
}
type CreateOrRotateTesterJoinLinkResult struct {
	result  domain.CreateOrRotateTesterJoinLinkResult
	joinURL string
}

func NewCreateOrRotateTesterJoinLinkResult(result *domain.CreateOrRotateTesterJoinLinkResult, joinURL string) (*CreateOrRotateTesterJoinLinkResult, error) {
	if result == nil || joinURL == "" {
		return nil, domain.NewInternalError(nil)
	}
	return &CreateOrRotateTesterJoinLinkResult{*result, joinURL}, nil
}
func (r *CreateOrRotateTesterJoinLinkResult) JoinLink() *domain.ApplicationTesterJoinLink {
	return r.result.JoinLink()
}
func (r *CreateOrRotateTesterJoinLinkResult) ReplacedJoinLinkID() *domain.ApplicationTesterJoinLinkID {
	return r.result.ReplacedJoinLinkID()
}
func (r *CreateOrRotateTesterJoinLinkResult) JoinURL() string { return r.joinURL }
func (r CreateOrRotateTesterJoinLinkResult) String() string {
	return "CreateOrRotateTesterJoinLinkResult{joinURL: [redacted]}"
}
func (r CreateOrRotateTesterJoinLinkResult) GoString() string { return r.String() }

type CreateOrRotateTesterJoinLinkHandler struct {
	tokens     port.SecureTesterJoinTokenFactory
	urls       port.TesterJoinURLBuilder
	ids        port.UUIDv7Generator
	clock      port.Clock
	repository port.ApplicationTesterJoinLinkRepository
}

func NewCreateOrRotateTesterJoinLinkHandler(tokens port.SecureTesterJoinTokenFactory, urls port.TesterJoinURLBuilder, ids port.UUIDv7Generator, clock port.Clock, repository port.ApplicationTesterJoinLinkRepository) *CreateOrRotateTesterJoinLinkHandler {
	return &CreateOrRotateTesterJoinLinkHandler{tokens, urls, ids, clock, repository}
}
func (h *CreateOrRotateTesterJoinLinkHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, command CreateOrRotateTesterJoinLinkCommand) (*CreateOrRotateTesterJoinLinkResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if !applicationID.IsValid() {
		return nil, domain.ErrInvalidApplicationId
	}
	applicationID = shared.ApplicationID(strings.ToLower(applicationID.String()))
	var expected *domain.ApplicationTesterJoinLinkID
	if command.ExpectedActiveJoinLinkID != nil {
		id, err := domain.ParseTesterJoinLinkID(command.ExpectedActiveJoinLinkID.String())
		if err != nil {
			return nil, err
		}
		expected = &id
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadCurrent(ctx, applicationID, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil || candidate.ApplicationID() != applicationID {
		return nil, domain.NewInternalError(nil)
	}
	if err = candidate.EnsureExpected(expected); err != nil {
		return nil, err
	}
	if h.tokens == nil || h.urls == nil || h.ids == nil || h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	rawToken, tokenHash, err := h.tokens.NewToken()
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	rawID, err := h.ids.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	id, err := domain.ParseTesterJoinLinkID(rawID)
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	link, err := domain.NewActiveTesterJoinLink(id, applicationID, domain.NewTesterJoinTokenHash(tokenHash), identity.AuthID, at)
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	joinURL, err := h.urls.Build(id, rawToken)
	if err != nil || joinURL == "" {
		return nil, domain.NewInternalError(nil)
	}
	// Only the persisted hash crosses this boundary. Generated credentials stay
	// local and are discarded if the final authority/concurrency check fails.
	result, err := h.repository.CreateOrRotate(ctx, applicationID, identity.AuthID, expected, link)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil || result.JoinLink().JoinLinkID() != id || result.JoinLink().ApplicationID() != applicationID || !equalIDs(result.ReplacedJoinLinkID(), expected) {
		return nil, domain.NewInternalError(nil)
	}
	return NewCreateOrRotateTesterJoinLinkResult(result, joinURL)
}
func equalIDs(a, b *domain.ApplicationTesterJoinLinkID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func mapRepositoryError(err error) error {
	for _, pair := range []struct{ source, want error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationTesterJoinLinkAlreadyExists, domain.ErrApplicationTesterJoinLinkAlreadyExists},
		{port.ErrApplicationTesterJoinLinkNotFound, domain.ErrApplicationTesterJoinLinkNotFound},
		{port.ErrApplicationTesterJoinLinkChanged, domain.ErrApplicationTesterJoinLinkChanged},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.want) {
			return pair.want
		}
	}
	// Persistence errors may carry rejected token hashes; retain no secret-bearing
	// cause in the command error that could enter ordinary logs or tracing.
	return domain.NewInternalError(nil)
}
