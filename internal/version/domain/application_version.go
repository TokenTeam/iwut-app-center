package domain

import (
	"time"

	"iwut-app-center/internal/shared"
)

type ReviewStatus string

const ReviewStatusDraft ReviewStatus = "DRAFT"

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
	if !id.IsValid() || !applicationID.IsValid() || !versionLabel.valid() || !launchURL.valid() ||
		!rpcAPIRange.valid() || !requiredCapabilities.valid() || !scopeRequest.valid() ||
		!createdBy.IsValid() || createdAt.IsZero() {
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
func (draft *DraftApplicationVersion) CreatedBy() shared.AuthID   { return draft.createdBy }
func (draft *DraftApplicationVersion) CreatedAt() time.Time       { return draft.createdAt }
func (draft *DraftApplicationVersion) ReviewStatus() ReviewStatus { return ReviewStatusDraft }
func (draft *DraftApplicationVersion) Revision() int64            { return 1 }
func (draft *DraftApplicationVersion) UpdatedBy() shared.AuthID   { return draft.createdBy }
func (draft *DraftApplicationVersion) UpdatedAt() time.Time       { return draft.createdAt }

type ApplicationVersion struct {
	draft    DraftApplicationVersion
	sequence VersionSequence
}

func NewApplicationVersion(draft *DraftApplicationVersion, sequence VersionSequence) (*ApplicationVersion, error) {
	if draft == nil || sequence < 1 {
		return nil, NewInternalError(nil)
	}
	copy := *draft
	return &ApplicationVersion{draft: copy, sequence: sequence}, nil
}

func (version *ApplicationVersion) ID() ApplicationVersionID { return version.draft.ID() }
func (version *ApplicationVersion) ApplicationID() shared.ApplicationID {
	return version.draft.ApplicationID()
}
func (version *ApplicationVersion) Sequence() VersionSequence  { return version.sequence }
func (version *ApplicationVersion) VersionLabel() VersionLabel { return version.draft.VersionLabel() }
func (version *ApplicationVersion) LaunchURL() LaunchURL       { return version.draft.LaunchURL() }
func (version *ApplicationVersion) RPCApiRange() RPCApiRange   { return version.draft.RPCApiRange() }
func (version *ApplicationVersion) RequiredCapabilities() []CapabilityName {
	return version.draft.RequiredCapabilities()
}
func (version *ApplicationVersion) RequiredScopes() []ScopeName {
	return version.draft.RequiredScopes()
}
func (version *ApplicationVersion) OptionalScopes() []ScopeName {
	return version.draft.OptionalScopes()
}
func (version *ApplicationVersion) ReviewStatus() ReviewStatus { return version.draft.ReviewStatus() }
func (version *ApplicationVersion) CreatedBy() shared.AuthID   { return version.draft.CreatedBy() }
func (version *ApplicationVersion) CreatedAt() time.Time       { return version.draft.CreatedAt() }
func (version *ApplicationVersion) Revision() int64            { return version.draft.Revision() }
func (version *ApplicationVersion) UpdatedBy() shared.AuthID   { return version.draft.UpdatedBy() }
func (version *ApplicationVersion) UpdatedAt() time.Time       { return version.draft.UpdatedAt() }
