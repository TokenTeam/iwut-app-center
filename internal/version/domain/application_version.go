package domain

import (
	"slices"
	"time"

	"iwut-app-center/internal/shared"
)

type ReviewStatus string

const (
	ReviewStatusDraft     ReviewStatus = "DRAFT"
	ReviewStatusSubmitted ReviewStatus = "SUBMITTED"
	ReviewStatusApproved  ReviewStatus = "APPROVED"
	ReviewStatusRejected  ReviewStatus = "REJECTED"
	ReviewStatusRevoked   ReviewStatus = "REVOKED"
)

func (status ReviewStatus) valid() bool {
	switch status {
	case ReviewStatusDraft, ReviewStatusSubmitted, ReviewStatusApproved, ReviewStatusRejected, ReviewStatusRevoked:
		return true
	default:
		return false
	}
}

type VersionSequence int32

func NewVersionSequence(value int32) (VersionSequence, error) {
	if value < 1 {
		return 0, NewInternalError(nil)
	}
	return VersionSequence(value), nil
}

func (sequence VersionSequence) Int32() int32 { return int32(sequence) }

// DraftApplicationVersion is the immutable, sequence-less draft passed to the
// repository. The repository assigns the per-Application sequence atomically.
type DraftApplicationVersion struct {
	id                   ApplicationVersionID
	applicationID        shared.ApplicationID
	versionLabel         VersionLabel
	launchURL            LaunchURL
	rpcAPIRange          RPCApiRange
	requiredCapabilities CapabilitySet
	scopeRequest         ScopeRequest
	oauthRedirects       OAuthRedirectConfiguration
	createdBy            shared.AuthID
	createdAt            time.Time
}

func NewDraftApplicationVersion(
	id ApplicationVersionID,
	applicationID shared.ApplicationID,
	versionLabel VersionLabel,
	launchURL LaunchURL,
	rpcAPIRange RPCApiRange,
	requiredCapabilities CapabilitySet,
	scopeRequest ScopeRequest,
	createdBy shared.AuthID,
	createdAt time.Time,
) (*DraftApplicationVersion, error) {
	return NewDraftApplicationVersionWithOAuth(id, applicationID, versionLabel, launchURL, rpcAPIRange, requiredCapabilities, scopeRequest, EmptyOAuthRedirectConfiguration(), createdBy, createdAt)
}

func NewDraftApplicationVersionWithOAuth(
	id ApplicationVersionID,
	applicationID shared.ApplicationID,
	versionLabel VersionLabel,
	launchURL LaunchURL,
	rpcAPIRange RPCApiRange,
	requiredCapabilities CapabilitySet,
	scopeRequest ScopeRequest,
	oauthRedirects OAuthRedirectConfiguration,
	createdBy shared.AuthID,
	createdAt time.Time,
) (*DraftApplicationVersion, error) {
	if !id.IsValid() || !applicationID.IsValid() || !versionLabel.valid() || !launchURL.valid() ||
		!rpcAPIRange.valid() || !requiredCapabilities.valid() || !scopeRequest.valid() ||
		!oauthRedirects.valid() || !createdBy.IsValid() || createdAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &DraftApplicationVersion{
		id:                   id,
		applicationID:        applicationID,
		versionLabel:         versionLabel,
		launchURL:            launchURL,
		rpcAPIRange:          rpcAPIRange,
		requiredCapabilities: requiredCapabilities,
		scopeRequest:         scopeRequest,
		oauthRedirects:       oauthRedirects,
		createdBy:            createdBy,
		createdAt:            createdAt.UTC(),
	}, nil
}

func (draft *DraftApplicationVersion) ID() ApplicationVersionID { return draft.id }
func (draft *DraftApplicationVersion) ApplicationID() shared.ApplicationID {
	return draft.applicationID
}
func (draft *DraftApplicationVersion) VersionLabel() VersionLabel { return draft.versionLabel }
func (draft *DraftApplicationVersion) LaunchURL() LaunchURL       { return draft.launchURL }
func (draft *DraftApplicationVersion) RPCApiRange() RPCApiRange   { return draft.rpcAPIRange }
func (draft *DraftApplicationVersion) RequiredCapabilities() []CapabilityName {
	return draft.requiredCapabilities.Values()
}
func (draft *DraftApplicationVersion) RequiredScopes() []ScopeName {
	return draft.scopeRequest.Required()
}
func (draft *DraftApplicationVersion) OptionalScopes() []ScopeName {
	return draft.scopeRequest.Optional()
}
func (draft *DraftApplicationVersion) OAuthRedirects() OAuthRedirectConfiguration {
	return draft.oauthRedirects
}
func (draft *DraftApplicationVersion) CreatedBy() shared.AuthID   { return draft.createdBy }
func (draft *DraftApplicationVersion) CreatedAt() time.Time       { return draft.createdAt }
func (draft *DraftApplicationVersion) ReviewStatus() ReviewStatus { return ReviewStatusDraft }
func (draft *DraftApplicationVersion) Revision() int64            { return 1 }
func (draft *DraftApplicationVersion) UpdatedBy() shared.AuthID   { return draft.createdBy }
func (draft *DraftApplicationVersion) UpdatedAt() time.Time       { return draft.createdAt }

type DraftApplicationVersionReplacement struct {
	versionLabel         VersionLabel
	launchURL            LaunchURL
	rpcAPIRange          RPCApiRange
	requiredCapabilities CapabilitySet
	scopeRequest         ScopeRequest
	oauthRedirects       OAuthRedirectConfiguration
}

func NewDraftApplicationVersionReplacement(
	versionLabel VersionLabel,
	launchURL LaunchURL,
	rpcAPIRange RPCApiRange,
	requiredCapabilities CapabilitySet,
	scopeRequest ScopeRequest,
) (DraftApplicationVersionReplacement, error) {
	return NewDraftApplicationVersionReplacementWithOAuth(versionLabel, launchURL, rpcAPIRange, requiredCapabilities, scopeRequest, EmptyOAuthRedirectConfiguration())
}

func NewDraftApplicationVersionReplacementWithOAuth(
	versionLabel VersionLabel,
	launchURL LaunchURL,
	rpcAPIRange RPCApiRange,
	requiredCapabilities CapabilitySet,
	scopeRequest ScopeRequest,
	oauthRedirects OAuthRedirectConfiguration,
) (DraftApplicationVersionReplacement, error) {
	if !versionLabel.valid() || !launchURL.valid() || !rpcAPIRange.valid() ||
		!requiredCapabilities.valid() || !scopeRequest.valid() || !oauthRedirects.valid() {
		return DraftApplicationVersionReplacement{}, NewInternalError(nil)
	}
	return DraftApplicationVersionReplacement{
		versionLabel:         versionLabel,
		launchURL:            launchURL,
		rpcAPIRange:          rpcAPIRange,
		requiredCapabilities: requiredCapabilities,
		scopeRequest:         scopeRequest,
		oauthRedirects:       oauthRedirects,
	}, nil
}

func (replacement DraftApplicationVersionReplacement) VersionLabel() VersionLabel {
	return replacement.versionLabel
}
func (replacement DraftApplicationVersionReplacement) LaunchURL() LaunchURL {
	return replacement.launchURL
}
func (replacement DraftApplicationVersionReplacement) RPCApiRange() RPCApiRange {
	return replacement.rpcAPIRange
}
func (replacement DraftApplicationVersionReplacement) RequiredCapabilities() []CapabilityName {
	return replacement.requiredCapabilities.Values()
}
func (replacement DraftApplicationVersionReplacement) RequiredScopes() []ScopeName {
	return replacement.scopeRequest.Required()
}
func (replacement DraftApplicationVersionReplacement) OptionalScopes() []ScopeName {
	return replacement.scopeRequest.Optional()
}
func (replacement DraftApplicationVersionReplacement) OAuthRedirects() OAuthRedirectConfiguration {
	return replacement.oauthRedirects
}

type ApplicationVersion struct {
	id                   ApplicationVersionID
	applicationID        shared.ApplicationID
	sequence             VersionSequence
	versionLabel         VersionLabel
	launchURL            LaunchURL
	rpcAPIRange          RPCApiRange
	requiredCapabilities CapabilitySet
	scopeRequest         ScopeRequest
	oauthRedirects       OAuthRedirectConfiguration
	reviewStatus         ReviewStatus
	createdBy            shared.AuthID
	createdAt            time.Time
	revision             int64
	updatedBy            shared.AuthID
	updatedAt            time.Time
}

func NewApplicationVersion(draft *DraftApplicationVersion, sequence VersionSequence) (*ApplicationVersion, error) {
	if draft == nil {
		return nil, NewInternalError(nil)
	}
	return RestoreApplicationVersion(
		draft.ID(), draft.ApplicationID(), sequence, draft.VersionLabel(), draft.LaunchURL(), draft.RPCApiRange(),
		draft.requiredCapabilities, draft.scopeRequest, ReviewStatusDraft, draft.CreatedBy(), draft.CreatedAt(),
		1, draft.UpdatedBy(), draft.UpdatedAt(), draft.OAuthRedirects(),
	)
}

// RestoreApplicationVersion reconstructs a persisted version after every
// stored field has been converted to its domain value type by the adapter.
func RestoreApplicationVersion(
	id ApplicationVersionID,
	applicationID shared.ApplicationID,
	sequence VersionSequence,
	versionLabel VersionLabel,
	launchURL LaunchURL,
	rpcAPIRange RPCApiRange,
	requiredCapabilities CapabilitySet,
	scopeRequest ScopeRequest,
	reviewStatus ReviewStatus,
	createdBy shared.AuthID,
	createdAt time.Time,
	revision int64,
	updatedBy shared.AuthID,
	updatedAt time.Time,
	oauthRedirects ...OAuthRedirectConfiguration,
) (*ApplicationVersion, error) {
	redirects := EmptyOAuthRedirectConfiguration()
	if len(oauthRedirects) == 1 {
		redirects = oauthRedirects[0]
	} else if len(oauthRedirects) > 1 {
		return nil, NewInternalError(nil)
	}
	if !id.IsValid() || !applicationID.IsValid() || sequence < 1 || !versionLabel.valid() || !launchURL.valid() ||
		!rpcAPIRange.valid() || !requiredCapabilities.valid() || !scopeRequest.valid() || !reviewStatus.valid() ||
		!redirects.valid() || !createdBy.IsValid() || createdAt.IsZero() || revision < 1 || !updatedBy.IsValid() || updatedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &ApplicationVersion{
		id: id, applicationID: applicationID, sequence: sequence, versionLabel: versionLabel, launchURL: launchURL,
		rpcAPIRange: rpcAPIRange, requiredCapabilities: requiredCapabilities, scopeRequest: scopeRequest,
		oauthRedirects: redirects,
		reviewStatus:   reviewStatus, createdBy: createdBy, createdAt: createdAt.UTC(), revision: revision,
		updatedBy: updatedBy, updatedAt: updatedAt.UTC(),
	}, nil
}

func (version *ApplicationVersion) ReplaceDraft(
	expectedRevision int64,
	replacement DraftApplicationVersionReplacement,
	updatedBy shared.AuthID,
	updatedAt time.Time,
) (*ApplicationVersion, error) {
	if version == nil {
		return nil, NewInternalError(nil)
	}
	if expectedRevision < 1 {
		return nil, ErrApplicationVersionRevisionRequired
	}
	if version.reviewStatus != ReviewStatusDraft {
		return nil, ErrApplicationVersionNotDraft
	}
	if version.revision != expectedRevision {
		return nil, ErrApplicationVersionRevisionConflict
	}
	if !replacement.versionLabel.valid() || !replacement.launchURL.valid() || !replacement.rpcAPIRange.valid() ||
		!replacement.requiredCapabilities.valid() || !replacement.scopeRequest.valid() ||
		!replacement.oauthRedirects.valid() ||
		!updatedBy.IsValid() || updatedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	if version.hasReplacement(replacement) {
		copy := *version
		return &copy, nil
	}

	copy := *version
	copy.versionLabel = replacement.versionLabel
	copy.launchURL = replacement.launchURL
	copy.rpcAPIRange = replacement.rpcAPIRange
	copy.requiredCapabilities = replacement.requiredCapabilities
	copy.scopeRequest = replacement.scopeRequest
	copy.oauthRedirects = replacement.oauthRedirects
	copy.revision++
	copy.updatedBy = updatedBy
	copy.updatedAt = updatedAt.UTC()
	return &copy, nil
}

func (version *ApplicationVersion) RestoreDraft(
	expectedRevision int64,
	restoredBy shared.AuthID,
	restoredAt time.Time,
) (*ApplicationVersion, error) {
	if version == nil {
		return nil, NewInternalError(nil)
	}
	if expectedRevision < 1 {
		return nil, ErrApplicationVersionRevisionRequired
	}
	if version.reviewStatus != ReviewStatusRejected {
		return nil, ErrApplicationVersionNotRejected
	}
	if version.revision != expectedRevision {
		return nil, ErrApplicationVersionRevisionConflict
	}
	if !restoredBy.IsValid() || restoredAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	copy := *version
	copy.reviewStatus = ReviewStatusDraft
	copy.revision++
	copy.updatedBy = restoredBy
	copy.updatedAt = restoredAt.UTC()
	return &copy, nil
}

func (version *ApplicationVersion) hasReplacement(replacement DraftApplicationVersionReplacement) bool {
	return version.versionLabel == replacement.versionLabel &&
		version.launchURL == replacement.launchURL &&
		version.rpcAPIRange == replacement.rpcAPIRange &&
		slices.Equal(version.requiredCapabilities.values, replacement.requiredCapabilities.values) &&
		slices.Equal(version.scopeRequest.required, replacement.scopeRequest.required) &&
		slices.Equal(version.scopeRequest.optional, replacement.scopeRequest.optional) &&
		version.oauthRedirects.Equal(replacement.oauthRedirects)
}

func (version *ApplicationVersion) ID() ApplicationVersionID { return version.id }
func (version *ApplicationVersion) ApplicationID() shared.ApplicationID {
	return version.applicationID
}
func (version *ApplicationVersion) Sequence() VersionSequence  { return version.sequence }
func (version *ApplicationVersion) VersionLabel() VersionLabel { return version.versionLabel }
func (version *ApplicationVersion) LaunchURL() LaunchURL       { return version.launchURL }
func (version *ApplicationVersion) RPCApiRange() RPCApiRange   { return version.rpcAPIRange }
func (version *ApplicationVersion) RequiredCapabilities() []CapabilityName {
	return version.requiredCapabilities.Values()
}
func (version *ApplicationVersion) RequiredScopes() []ScopeName {
	return version.scopeRequest.Required()
}
func (version *ApplicationVersion) OptionalScopes() []ScopeName {
	return version.scopeRequest.Optional()
}
func (version *ApplicationVersion) OAuthRedirects() OAuthRedirectConfiguration {
	return version.oauthRedirects
}
func (version *ApplicationVersion) ReviewStatus() ReviewStatus { return version.reviewStatus }
func (version *ApplicationVersion) CreatedBy() shared.AuthID   { return version.createdBy }
func (version *ApplicationVersion) CreatedAt() time.Time       { return version.createdAt }
func (version *ApplicationVersion) Revision() int64            { return version.revision }
func (version *ApplicationVersion) UpdatedBy() shared.AuthID   { return version.updatedBy }
func (version *ApplicationVersion) UpdatedAt() time.Time       { return version.updatedAt }
