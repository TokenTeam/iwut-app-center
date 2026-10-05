package usecase

import (
	"context"
	"encoding/base64"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
)

type JoinApplicationAsTesterCommand struct{ Secret string }

func (c JoinApplicationAsTesterCommand) String() string {
	return "JoinApplicationAsTesterCommand{secret: [redacted]}"
}
func (c JoinApplicationAsTesterCommand) GoString() string { return c.String() }

type JoinApplicationAsTesterHandler struct {
	hasher     port.TesterJoinTokenHasher
	ids        port.UUIDv7Generator
	clock      port.Clock
	repository port.ApplicationTesterMembershipRepository
}

func NewJoinApplicationAsTesterHandler(hasher port.TesterJoinTokenHasher, ids port.UUIDv7Generator, clock port.Clock, repository port.ApplicationTesterMembershipRepository) *JoinApplicationAsTesterHandler {
	return &JoinApplicationAsTesterHandler{hasher, ids, clock, repository}
}
func (h *JoinApplicationAsTesterHandler) Handle(ctx context.Context, identity shared.AuthenticatedUserIdentity, linkID domain.ApplicationTesterJoinLinkID, command JoinApplicationAsTesterCommand) (*domain.JoinApplicationAsTesterResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrAuthenticatedUserRequired
	}
	linkID, err := domain.ParseTesterJoinLinkID(linkID.String())
	if err != nil {
		return nil, err
	}
	// Round-trip validation rejects padding, CR/LF ignored by Go's decoder,
	// noncanonical trailing bits and every encoding other than raw base64url.
	if len(command.Secret) != 43 {
		return nil, domain.ErrInvalidTesterJoinSecret
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(command.Secret)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != command.Secret {
		return nil, domain.ErrInvalidTesterJoinSecret
	}
	if h == nil || h.hasher == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	var raw [32]byte
	copy(raw[:], decoded)
	hash := h.hasher.Hash(raw)
	candidate, err := h.repository.ResolveJoinCandidate(ctx, linkID, hash)
	if err != nil {
		return nil, mapMembershipRepositoryError(err)
	}
	if candidate == nil || !candidate.ApplicationID().IsValid() || h.ids == nil || h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	value, err := h.ids.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	id, err := domain.ParseTesterMembershipID(value)
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	membership, err := domain.NewActiveTesterMembership(id, candidate.ApplicationID(), identity.AuthID, linkID, h.clock.Now())
	if err != nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.Join(ctx, linkID, hash, identity.AuthID, membership, domain.ApplicationTesterLimit)
	if err != nil {
		return nil, mapMembershipRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	stored := result.Membership()
	if stored == nil || stored.ApplicationID() != candidate.ApplicationID() || stored.TesterAuthID() != identity.AuthID || stored.Status() != domain.MembershipStatusActive || result.TesterLimit() != domain.ApplicationTesterLimit || result.ActiveTesterCount() < 1 || result.ActiveTesterCount() > domain.ApplicationTesterLimit {
		return nil, domain.NewInternalError(nil)
	}
	if result.Joined() && (stored.MembershipID() != membership.MembershipID() || stored.JoinedViaJoinLinkID() != linkID) {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
func mapMembershipRepositoryError(err error) error {
	if errors.Is(err, shared.ErrAccountExitBlocked) {
		return shared.ErrAccountExitBlocked
	}
	for _, pair := range []struct{ source, target error }{
		{port.ErrTesterJoinLinkInvalid, domain.ErrTesterJoinLinkInvalid},
		{port.ErrApplicationTesterLimitReached, domain.ErrApplicationTesterLimitReached},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	// Driver causes may contain a credential hash; do not retain them.
	return domain.NewInternalError(nil)
}
