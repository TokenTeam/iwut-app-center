package domain

import (
	"regexp"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

var policyVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,50}$`)

type ApplicationReviewID string

func (id ApplicationReviewID) String() string { return string(id) }
func (id ApplicationReviewID) IsValid() bool  { return isUUIDv7(string(id)) }

type ApplicationVersionID string

func (id ApplicationVersionID) String() string { return string(id) }
func (id ApplicationVersionID) IsValid() bool  { return isUUIDv7(string(id)) }

type ReviewAttempt int32

func NewReviewAttempt(value int32) (ReviewAttempt, error) {
	if value < 1 {
		return 0, NewInternalError(nil)
	}
	return ReviewAttempt(value), nil
}

func (attempt ReviewAttempt) Int32() int32 { return int32(attempt) }

type ScopeCatalogRevision int64

func NewScopeCatalogRevision(value int64) (ScopeCatalogRevision, error) {
	if value < 1 {
		return 0, NewInternalError(nil)
	}
	return ScopeCatalogRevision(value), nil
}

func (revision ScopeCatalogRevision) Int64() int64 { return int64(revision) }

type PreflightPolicyVersion string

func NewPreflightPolicyVersion(value string) (PreflightPolicyVersion, error) {
	if !policyVersionPattern.MatchString(value) {
		return "", NewInternalError(nil)
	}
	return PreflightPolicyVersion(value), nil
}

func (version PreflightPolicyVersion) String() string { return string(version) }

type ScopeName string
type LaunchURL string

type OAuthRedirectConfiguration struct {
	pkceRedirectURIs         []string
	confidentialRedirectURIs []string
}

func NewOAuthRedirectConfiguration(pkce, confidential []string) (OAuthRedirectConfiguration, error) {
	if pkce == nil || confidential == nil || len(pkce) > 10 || len(confidential) > 10 ||
		!strictlySortedUnique(pkce) || !strictlySortedUnique(confidential) || hasOverlap(pkce, confidential) {
		return OAuthRedirectConfiguration{}, NewInternalError(nil)
	}
	return OAuthRedirectConfiguration{
		pkceRedirectURIs:         append([]string{}, pkce...),
		confidentialRedirectURIs: append([]string{}, confidential...),
	}, nil
}

func EmptyOAuthRedirectConfiguration() OAuthRedirectConfiguration {
	configuration, _ := NewOAuthRedirectConfiguration([]string{}, []string{})
	return configuration
}

func (configuration OAuthRedirectConfiguration) PKCERedirectURIs() []string {
	return append([]string{}, configuration.pkceRedirectURIs...)
}

func (configuration OAuthRedirectConfiguration) ConfidentialRedirectURIs() []string {
	return append([]string{}, configuration.confidentialRedirectURIs...)
}

func (configuration OAuthRedirectConfiguration) Equal(other OAuthRedirectConfiguration) bool {
	return slices.Equal(configuration.pkceRedirectURIs, other.pkceRedirectURIs) &&
		slices.Equal(configuration.confidentialRedirectURIs, other.confidentialRedirectURIs)
}

type ReviewStatus string

const (
	ReviewStatusPending  ReviewStatus = "PENDING"
	ReviewStatusApproved ReviewStatus = "APPROVED"
	ReviewStatusRejected ReviewStatus = "REJECTED"
)

func (status ReviewStatus) valid() bool {
	switch status {
	case ReviewStatusPending, ReviewStatusApproved, ReviewStatusRejected:
		return true
	default:
		return false
	}
}

// SubmittedVersionReviewStatus is the ApplicationVersion lifecycle value a
// decidable Review must observe.
const SubmittedVersionReviewStatus = "SUBMITTED"

type ApplicationVersionReviewSnapshot struct {
	versionLabel              string
	launchURL                 LaunchURL
	rpcAPIMinVersion          int32
	rpcAPIMaxVersionExclusive int32
	requiredCapabilities      []string
	requiredScopes            []ScopeName
	optionalScopes            []ScopeName
	oauthRedirects            OAuthRedirectConfiguration
}

func NewApplicationVersionReviewSnapshot(
	versionLabel string,
	launchURL LaunchURL,
	rpcAPIMinVersion int32,
	rpcAPIMaxVersionExclusive int32,
	requiredCapabilities []string,
	requiredScopes []ScopeName,
	optionalScopes []ScopeName,
	oauthRedirects ...OAuthRedirectConfiguration,
) (*ApplicationVersionReviewSnapshot, error) {
	redirects := EmptyOAuthRedirectConfiguration()
	if len(oauthRedirects) == 1 {
		redirects = OAuthRedirectConfiguration{
			pkceRedirectURIs:         oauthRedirects[0].PKCERedirectURIs(),
			confidentialRedirectURIs: oauthRedirects[0].ConfidentialRedirectURIs(),
		}
	} else if len(oauthRedirects) > 1 {
		return nil, NewInternalError(nil)
	}
	if versionLabel == "" || !utf8.ValidString(versionLabel) || launchURL == "" ||
		rpcAPIMinVersion < 1 || rpcAPIMaxVersionExclusive <= rpcAPIMinVersion ||
		!strictlySortedUnique(requiredCapabilities) || !strictlySortedUnique(requiredScopes) ||
		!strictlySortedUnique(optionalScopes) || hasOverlap(requiredScopes, optionalScopes) ||
		!strictlySortedUnique(redirects.pkceRedirectURIs) || !strictlySortedUnique(redirects.confidentialRedirectURIs) ||
		hasOverlap(redirects.pkceRedirectURIs, redirects.confidentialRedirectURIs) {
		return nil, NewInternalError(nil)
	}
	return &ApplicationVersionReviewSnapshot{
		versionLabel:              versionLabel,
		launchURL:                 launchURL,
		rpcAPIMinVersion:          rpcAPIMinVersion,
		rpcAPIMaxVersionExclusive: rpcAPIMaxVersionExclusive,
		requiredCapabilities:      append([]string{}, requiredCapabilities...),
		requiredScopes:            append([]ScopeName{}, requiredScopes...),
		optionalScopes:            append([]ScopeName{}, optionalScopes...),
		oauthRedirects:            redirects,
	}, nil
}

func (snapshot ApplicationVersionReviewSnapshot) VersionLabel() string { return snapshot.versionLabel }
func (snapshot ApplicationVersionReviewSnapshot) LaunchURL() LaunchURL { return snapshot.launchURL }
func (snapshot ApplicationVersionReviewSnapshot) RPCAPIMinVersion() int32 {
	return snapshot.rpcAPIMinVersion
}
func (snapshot ApplicationVersionReviewSnapshot) RPCAPIMaxVersionExclusive() int32 {
	return snapshot.rpcAPIMaxVersionExclusive
}
func (snapshot ApplicationVersionReviewSnapshot) RequiredCapabilities() []string {
	return append([]string{}, snapshot.requiredCapabilities...)
}
func (snapshot ApplicationVersionReviewSnapshot) RequiredScopes() []ScopeName {
	return append([]ScopeName{}, snapshot.requiredScopes...)
}
func (snapshot ApplicationVersionReviewSnapshot) OptionalScopes() []ScopeName {
	return append([]ScopeName{}, snapshot.optionalScopes...)
}
func (snapshot ApplicationVersionReviewSnapshot) OAuthRedirects() OAuthRedirectConfiguration {
	return OAuthRedirectConfiguration{
		pkceRedirectURIs:         snapshot.oauthRedirects.PKCERedirectURIs(),
		confidentialRedirectURIs: snapshot.oauthRedirects.ConfidentialRedirectURIs(),
	}
}

// Equal reports whether two snapshots carry exactly the same reviewed content.
func (snapshot ApplicationVersionReviewSnapshot) Equal(other ApplicationVersionReviewSnapshot) bool {
	return snapshot.versionLabel == other.versionLabel &&
		snapshot.launchURL == other.launchURL &&
		snapshot.rpcAPIMinVersion == other.rpcAPIMinVersion &&
		snapshot.rpcAPIMaxVersionExclusive == other.rpcAPIMaxVersionExclusive &&
		slices.Equal(snapshot.requiredCapabilities, other.requiredCapabilities) &&
		slices.Equal(snapshot.requiredScopes, other.requiredScopes) &&
		slices.Equal(snapshot.optionalScopes, other.optionalScopes) &&
		snapshot.oauthRedirects.Equal(other.oauthRedirects)
}

type SubmissionCandidate struct {
	applicationID shared.ApplicationID
	versionID     ApplicationVersionID
	revision      int64
	snapshot      ApplicationVersionReviewSnapshot
}

func NewSubmissionCandidate(
	applicationID shared.ApplicationID,
	versionID ApplicationVersionID,
	revision int64,
	snapshot *ApplicationVersionReviewSnapshot,
) (*SubmissionCandidate, error) {
	if !applicationID.IsValid() || !versionID.IsValid() || revision < 1 || snapshot == nil {
		return nil, NewInternalError(nil)
	}
	return &SubmissionCandidate{
		applicationID: applicationID,
		versionID:     versionID,
		revision:      revision,
		snapshot:      *snapshot,
	}, nil
}

func (candidate *SubmissionCandidate) ApplicationID() shared.ApplicationID {
	return candidate.applicationID
}
func (candidate *SubmissionCandidate) VersionID() ApplicationVersionID { return candidate.versionID }
func (candidate *SubmissionCandidate) Revision() int64                 { return candidate.revision }
func (candidate *SubmissionCandidate) Snapshot() ApplicationVersionReviewSnapshot {
	copy := candidate.snapshot
	copy.requiredCapabilities = candidate.snapshot.RequiredCapabilities()
	copy.requiredScopes = candidate.snapshot.RequiredScopes()
	copy.optionalScopes = candidate.snapshot.OptionalScopes()
	copy.oauthRedirects = candidate.snapshot.OAuthRedirects()
	return copy
}
func (candidate *SubmissionCandidate) AllScopes() []ScopeName {
	values := append(candidate.snapshot.RequiredScopes(), candidate.snapshot.OptionalScopes()...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

type ApplicationReview struct {
	reviewID               ApplicationReviewID
	applicationID          shared.ApplicationID
	versionID              ApplicationVersionID
	attempt                ReviewAttempt
	sourceVersionRevision  int64
	snapshot               ApplicationVersionReviewSnapshot
	scopeCatalogRevision   ScopeCatalogRevision
	preflightPolicyVersion PreflightPolicyVersion
	submittedBy            shared.AuthID
	submittedAt            time.Time
	status                 ReviewStatus
	decision               *ApplicationReviewDecision
	draftRestoration       *ApplicationReviewDraftRestoration
}

type ApplicationReviewDraftRestoration struct {
	restoredBy            shared.AuthID
	restoredAt            time.Time
	resultVersionRevision int64
}

func NewApplicationReviewDraftRestoration(
	restoredBy shared.AuthID,
	restoredAt time.Time,
	resultVersionRevision int64,
) (*ApplicationReviewDraftRestoration, error) {
	if !restoredBy.IsValid() || restoredAt.IsZero() || resultVersionRevision < 1 {
		return nil, NewInternalError(nil)
	}
	return &ApplicationReviewDraftRestoration{
		restoredBy:            restoredBy,
		restoredAt:            restoredAt.UTC(),
		resultVersionRevision: resultVersionRevision,
	}, nil
}

func (restoration *ApplicationReviewDraftRestoration) RestoredBy() shared.AuthID {
	return restoration.restoredBy
}
func (restoration *ApplicationReviewDraftRestoration) RestoredAt() time.Time {
	return restoration.restoredAt
}
func (restoration *ApplicationReviewDraftRestoration) ResultVersionRevision() int64 {
	return restoration.resultVersionRevision
}

func NewPendingApplicationReview(
	candidate *SubmissionCandidate,
	reviewID ApplicationReviewID,
	attempt ReviewAttempt,
	scopeCatalogRevision ScopeCatalogRevision,
	preflightPolicyVersion PreflightPolicyVersion,
	submittedBy shared.AuthID,
	submittedAt time.Time,
) (*ApplicationReview, error) {
	if candidate == nil || !reviewID.IsValid() || attempt < 1 || scopeCatalogRevision < 1 ||
		!policyVersionPattern.MatchString(preflightPolicyVersion.String()) || !submittedBy.IsValid() || submittedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &ApplicationReview{
		reviewID:               reviewID,
		applicationID:          candidate.ApplicationID(),
		versionID:              candidate.VersionID(),
		attempt:                attempt,
		sourceVersionRevision:  candidate.Revision(),
		snapshot:               candidate.Snapshot(),
		scopeCatalogRevision:   scopeCatalogRevision,
		preflightPolicyVersion: preflightPolicyVersion,
		submittedBy:            submittedBy,
		submittedAt:            submittedAt.UTC(),
		status:                 ReviewStatusPending,
	}, nil
}

// RestoreApplicationReview reconstructs a persisted Review after every stored
// field has been converted to its domain value type by the adapter.
func RestoreApplicationReview(
	reviewID ApplicationReviewID,
	applicationID shared.ApplicationID,
	versionID ApplicationVersionID,
	attempt ReviewAttempt,
	sourceVersionRevision int64,
	snapshot ApplicationVersionReviewSnapshot,
	scopeCatalogRevision ScopeCatalogRevision,
	preflightPolicyVersion PreflightPolicyVersion,
	submittedBy shared.AuthID,
	submittedAt time.Time,
	status ReviewStatus,
	decision *ApplicationReviewDecision,
	draftRestoration *ApplicationReviewDraftRestoration,
) (*ApplicationReview, error) {
	if !reviewID.IsValid() || !applicationID.IsValid() || !versionID.IsValid() || attempt < 1 ||
		sourceVersionRevision < 1 || scopeCatalogRevision < 1 ||
		!policyVersionPattern.MatchString(preflightPolicyVersion.String()) || !submittedBy.IsValid() ||
		submittedAt.IsZero() || !status.valid() {
		return nil, NewInternalError(nil)
	}
	if status == ReviewStatusPending {
		if decision != nil {
			return nil, NewInternalError(nil)
		}
	} else if decision == nil || decision.Outcome() != ReviewDecision(status) {
		return nil, NewInternalError(nil)
	}
	if draftRestoration != nil && status != ReviewStatusRejected {
		return nil, NewInternalError(nil)
	}
	return &ApplicationReview{
		reviewID:               reviewID,
		applicationID:          applicationID,
		versionID:              versionID,
		attempt:                attempt,
		sourceVersionRevision:  sourceVersionRevision,
		snapshot:               snapshot,
		scopeCatalogRevision:   scopeCatalogRevision,
		preflightPolicyVersion: preflightPolicyVersion,
		submittedBy:            submittedBy,
		submittedAt:            submittedAt.UTC(),
		status:                 status,
		decision:               copyDecision(decision),
		draftRestoration:       copyDraftRestoration(draftRestoration),
	}, nil
}

func (review *ApplicationReview) ReviewID() ApplicationReviewID       { return review.reviewID }
func (review *ApplicationReview) ApplicationID() shared.ApplicationID { return review.applicationID }
func (review *ApplicationReview) VersionID() ApplicationVersionID     { return review.versionID }
func (review *ApplicationReview) Attempt() ReviewAttempt              { return review.attempt }
func (review *ApplicationReview) SourceVersionRevision() int64        { return review.sourceVersionRevision }
func (review *ApplicationReview) Status() ReviewStatus                { return review.status }
func (review *ApplicationReview) HasDecision() bool                   { return review.decision != nil }
func (review *ApplicationReview) HasDraftRestoration() bool {
	return review != nil && review.draftRestoration != nil
}
func (review *ApplicationReview) DraftRestoration() *ApplicationReviewDraftRestoration {
	return copyDraftRestoration(review.draftRestoration)
}
func (review *ApplicationReview) Decision() *ApplicationReviewDecision {
	return copyDecision(review.decision)
}
func (review *ApplicationReview) Snapshot() ApplicationVersionReviewSnapshot {
	copy := review.snapshot
	copy.requiredCapabilities = review.snapshot.RequiredCapabilities()
	copy.requiredScopes = review.snapshot.RequiredScopes()
	copy.optionalScopes = review.snapshot.OptionalScopes()
	copy.oauthRedirects = review.snapshot.OAuthRedirects()
	return copy
}
func (review *ApplicationReview) ScopeCatalogRevision() ScopeCatalogRevision {
	return review.scopeCatalogRevision
}
func (review *ApplicationReview) PreflightPolicyVersion() PreflightPolicyVersion {
	return review.preflightPolicyVersion
}
func (review *ApplicationReview) SubmittedBy() shared.AuthID { return review.submittedBy }
func (review *ApplicationReview) SubmittedAt() time.Time     { return review.submittedAt }

// Approve writes the single APPROVED decision for a PENDING attempt. A second
// call, or a decision stored on the entity, fails without mutating it.
func (review *ApplicationReview) Approve(
	policyVersion ReviewPolicyVersion,
	confirmedCheckIDs []ReviewCheckID,
	optionalReason string,
	approvalValidation *ApprovalValidation,
	decidedBy shared.AuthID,
	decidedAt time.Time,
) (*ApplicationReviewDecision, error) {
	if review == nil || review.status != ReviewStatusPending || review.decision != nil {
		return nil, ErrApplicationReviewAlreadyDecided
	}
	decision, err := NewApprovedDecision(
		policyVersion, confirmedCheckIDs, optionalReason, approvalValidation, decidedBy, decidedAt,
	)
	if err != nil {
		return nil, err
	}
	review.status = ReviewStatusApproved
	review.decision = decision
	return copyDecision(decision), nil
}

// Reject writes the single REJECTED decision for a PENDING attempt.
func (review *ApplicationReview) Reject(
	policyVersion ReviewPolicyVersion,
	reason string,
	decidedBy shared.AuthID,
	decidedAt time.Time,
) (*ApplicationReviewDecision, error) {
	if review == nil || review.status != ReviewStatusPending || review.decision != nil {
		return nil, ErrApplicationReviewAlreadyDecided
	}
	decision, err := NewRejectedDecision(policyVersion, reason, decidedBy, decidedAt)
	if err != nil {
		return nil, err
	}
	review.status = ReviewStatusRejected
	review.decision = decision
	return copyDecision(decision), nil
}

func (review *ApplicationReview) RecordDraftRestoration(
	restoredBy shared.AuthID,
	restoredAt time.Time,
	resultVersionRevision int64,
) (*ApplicationReviewDraftRestoration, error) {
	if review == nil || review.status != ReviewStatusRejected || review.decision == nil ||
		review.decision.Outcome() != ReviewDecisionRejected {
		return nil, ErrApplicationReviewStateInconsistent
	}
	if review.draftRestoration != nil {
		return nil, ErrApplicationReviewAlreadyRestored
	}
	if resultVersionRevision != review.sourceVersionRevision+3 {
		return nil, ErrApplicationReviewStateInconsistent
	}
	restoration, err := NewApplicationReviewDraftRestoration(restoredBy, restoredAt, resultVersionRevision)
	if err != nil {
		return nil, err
	}
	review.draftRestoration = restoration
	return copyDraftRestoration(restoration), nil
}

func copyDecision(decision *ApplicationReviewDecision) *ApplicationReviewDecision {
	if decision == nil {
		return nil
	}
	copy := *decision
	copy.confirmedCheckIDs = append([]ReviewCheckID{}, decision.confirmedCheckIDs...)
	copy.approvalValidation = copyApprovalValidation(decision.approvalValidation)
	return &copy
}

func copyDraftRestoration(restoration *ApplicationReviewDraftRestoration) *ApplicationReviewDraftRestoration {
	if restoration == nil {
		return nil
	}
	copy := *restoration
	return &copy
}

const RestoredVersionReviewStatus = "DRAFT"

type RestoredApplicationVersion struct {
	applicationID shared.ApplicationID
	versionID     ApplicationVersionID
	revision      int64
	updatedBy     shared.AuthID
	updatedAt     time.Time
}

func NewRestoredApplicationVersion(
	applicationID shared.ApplicationID,
	versionID ApplicationVersionID,
	revision int64,
	updatedBy shared.AuthID,
	updatedAt time.Time,
) (*RestoredApplicationVersion, error) {
	if !applicationID.IsValid() || !versionID.IsValid() || revision < 1 || !updatedBy.IsValid() || updatedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &RestoredApplicationVersion{
		applicationID: applicationID,
		versionID:     versionID,
		revision:      revision,
		updatedBy:     updatedBy,
		updatedAt:     updatedAt.UTC(),
	}, nil
}

func (version *RestoredApplicationVersion) ApplicationID() shared.ApplicationID {
	return version.applicationID
}
func (version *RestoredApplicationVersion) VersionID() ApplicationVersionID { return version.versionID }
func (version *RestoredApplicationVersion) ReviewStatus() string            { return RestoredVersionReviewStatus }
func (version *RestoredApplicationVersion) Revision() int64                 { return version.revision }
func (version *RestoredApplicationVersion) UpdatedBy() shared.AuthID        { return version.updatedBy }
func (version *RestoredApplicationVersion) UpdatedAt() time.Time            { return version.updatedAt }

type RestoreRejectedVersionResult struct {
	review  ApplicationReview
	version RestoredApplicationVersion
}

func NewRestoreRejectedVersionResult(
	review *ApplicationReview,
	version *RestoredApplicationVersion,
) (*RestoreRejectedVersionResult, error) {
	if review == nil || version == nil || review.Status() != ReviewStatusRejected || !review.HasDraftRestoration() ||
		review.ApplicationID() != version.ApplicationID() || review.VersionID() != version.VersionID() {
		return nil, NewInternalError(nil)
	}
	restoration := review.DraftRestoration()
	if restoration.ResultVersionRevision() != version.Revision() || restoration.RestoredBy() != version.UpdatedBy() ||
		!restoration.RestoredAt().Equal(version.UpdatedAt()) {
		return nil, NewInternalError(nil)
	}
	return &RestoreRejectedVersionResult{review: *review, version: *version}, nil
}

func (result *RestoreRejectedVersionResult) Review() *ApplicationReview {
	copy := result.review
	copy.snapshot = result.review.Snapshot()
	copy.decision = result.review.Decision()
	copy.draftRestoration = result.review.DraftRestoration()
	return &copy
}

func (result *RestoreRejectedVersionResult) Version() *RestoredApplicationVersion {
	copy := result.version
	return &copy
}

type SubmittedApplicationVersion struct {
	applicationID shared.ApplicationID
	versionID     ApplicationVersionID
	revision      int64
	updatedBy     shared.AuthID
	updatedAt     time.Time
}

func NewSubmittedApplicationVersion(candidate *SubmissionCandidate, updatedBy shared.AuthID, updatedAt time.Time) (*SubmittedApplicationVersion, error) {
	if candidate == nil || !updatedBy.IsValid() || updatedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &SubmittedApplicationVersion{
		applicationID: candidate.ApplicationID(), versionID: candidate.VersionID(), revision: candidate.Revision() + 1,
		updatedBy: updatedBy, updatedAt: updatedAt.UTC(),
	}, nil
}

func (version *SubmittedApplicationVersion) ApplicationID() shared.ApplicationID {
	return version.applicationID
}
func (version *SubmittedApplicationVersion) VersionID() ApplicationVersionID {
	return version.versionID
}
func (version *SubmittedApplicationVersion) ReviewStatus() string {
	return SubmittedVersionReviewStatus
}
func (version *SubmittedApplicationVersion) Revision() int64          { return version.revision }
func (version *SubmittedApplicationVersion) UpdatedBy() shared.AuthID { return version.updatedBy }
func (version *SubmittedApplicationVersion) UpdatedAt() time.Time     { return version.updatedAt }

type ReviewSubmissionResult struct {
	review  ApplicationReview
	version SubmittedApplicationVersion
}

func NewReviewSubmissionResult(review *ApplicationReview, version *SubmittedApplicationVersion) (*ReviewSubmissionResult, error) {
	if review == nil || version == nil || review.ApplicationID() != version.ApplicationID() || review.VersionID() != version.VersionID() ||
		review.SourceVersionRevision()+1 != version.Revision() || review.SubmittedBy() != version.UpdatedBy() || !review.SubmittedAt().Equal(version.UpdatedAt()) {
		return nil, NewInternalError(nil)
	}
	return &ReviewSubmissionResult{review: *review, version: *version}, nil
}

func (result *ReviewSubmissionResult) Review() *ApplicationReview {
	copy := result.review
	copy.snapshot = result.review.Snapshot()
	return &copy
}

func (result *ReviewSubmissionResult) Version() *SubmittedApplicationVersion {
	copy := result.version
	return &copy
}

func strictlySortedUnique[T ~string](values []T) bool {
	return values != nil && slices.IsSorted(values) && len(slices.Compact(append([]T{}, values...))) == len(values)
}

func hasOverlap[T ~string](left, right []T) bool {
	seen := make(map[T]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; ok {
			return true
		}
	}
	return false
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'A' && value[19] != 'b' && value[19] != 'B' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
