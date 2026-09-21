package domain

import (
	"slices"
	"time"

	"iwut-app-center/internal/shared"
)

// ApplicationReviewDecisionCandidate is the re-confirmable set of local facts a
// decision is based on. The Repository re-reads every fact inside the deciding
// transaction; the candidate is only the earlier observation.
type ApplicationReviewDecisionCandidate struct {
	review           *ApplicationReview
	versionRevision  int64
	versionCreatedBy shared.AuthID
	versionSnapshot  ApplicationVersionReviewSnapshot
	currentAdminID   shared.AuthID
}

func NewApplicationReviewDecisionCandidate(
	review *ApplicationReview,
	versionReviewStatus string,
	versionRevision int64,
	versionCreatedBy shared.AuthID,
	versionSnapshot *ApplicationVersionReviewSnapshot,
	currentAdminID shared.AuthID,
) (*ApplicationReviewDecisionCandidate, error) {
	if review == nil || review.status != ReviewStatusPending || review.decision != nil ||
		versionReviewStatus != SubmittedVersionReviewStatus || versionSnapshot == nil ||
		versionRevision != review.SourceVersionRevision()+1 ||
		!versionSnapshot.Equal(review.snapshot) ||
		!versionCreatedBy.IsValid() || !currentAdminID.IsValid() {
		return nil, NewInternalError(nil)
	}
	return &ApplicationReviewDecisionCandidate{
		review:           review,
		versionRevision:  versionRevision,
		versionCreatedBy: versionCreatedBy,
		versionSnapshot:  *versionSnapshot,
		currentAdminID:   currentAdminID,
	}, nil
}

func (candidate *ApplicationReviewDecisionCandidate) Review() *ApplicationReview {
	copy := *candidate.review
	copy.snapshot = candidate.review.Snapshot()
	copy.decision = copyDecision(candidate.review.decision)
	return &copy
}
func (candidate *ApplicationReviewDecisionCandidate) VersionRevision() int64 {
	return candidate.versionRevision
}
func (candidate *ApplicationReviewDecisionCandidate) VersionCreatedBy() shared.AuthID {
	return candidate.versionCreatedBy
}
func (candidate *ApplicationReviewDecisionCandidate) VersionSnapshot() ApplicationVersionReviewSnapshot {
	copy := candidate.versionSnapshot
	copy.requiredCapabilities = candidate.versionSnapshot.RequiredCapabilities()
	copy.requiredScopes = candidate.versionSnapshot.RequiredScopes()
	copy.optionalScopes = candidate.versionSnapshot.OptionalScopes()
	return copy
}
func (candidate *ApplicationReviewDecisionCandidate) CurrentAdminID() shared.AuthID {
	return candidate.currentAdminID
}

// AllScopes returns the reviewed required and optional scopes sorted by name.
func (candidate *ApplicationReviewDecisionCandidate) AllScopes() []ScopeName {
	values := append(candidate.review.Snapshot().RequiredScopes(), candidate.review.Snapshot().OptionalScopes()...)
	slices.Sort(values)
	return values
}

// ReviewerConflicts implements BR-REV-011 against the candidate's observed
// Application administrator.
func (candidate *ApplicationReviewDecisionCandidate) ReviewerConflicts(reviewerID shared.AuthID) bool {
	return reviewerID == candidate.currentAdminID ||
		reviewerID == candidate.versionCreatedBy ||
		reviewerID == candidate.review.SubmittedBy()
}

// DecidedApplicationVersion is the Version summary produced by a decision.
type DecidedApplicationVersion struct {
	applicationID shared.ApplicationID
	versionID     ApplicationVersionID
	reviewStatus  ReviewDecision
	revision      int64
	updatedBy     shared.AuthID
	updatedAt     time.Time
}

func NewDecidedApplicationVersion(
	candidate *ApplicationReviewDecisionCandidate,
	decision *ApplicationReviewDecision,
) (*DecidedApplicationVersion, error) {
	if candidate == nil || decision == nil {
		return nil, NewInternalError(nil)
	}
	review := candidate.Review()
	return &DecidedApplicationVersion{
		applicationID: review.ApplicationID(),
		versionID:     review.VersionID(),
		reviewStatus:  decision.Outcome(),
		revision:      candidate.VersionRevision() + 1,
		updatedBy:     decision.DecidedBy(),
		updatedAt:     decision.DecidedAt(),
	}, nil
}

func (version *DecidedApplicationVersion) ApplicationID() shared.ApplicationID {
	return version.applicationID
}
func (version *DecidedApplicationVersion) VersionID() ApplicationVersionID {
	return version.versionID
}
func (version *DecidedApplicationVersion) ReviewStatus() ReviewDecision { return version.reviewStatus }
func (version *DecidedApplicationVersion) Revision() int64              { return version.revision }
func (version *DecidedApplicationVersion) UpdatedBy() shared.AuthID     { return version.updatedBy }
func (version *DecidedApplicationVersion) UpdatedAt() time.Time         { return version.updatedAt }

// ApplicationReviewDecisionResult is the Review and Version summary returned by
// a successful decision.
type ApplicationReviewDecisionResult struct {
	review  ApplicationReview
	version DecidedApplicationVersion
}

func NewApplicationReviewDecisionResult(
	review *ApplicationReview,
	version *DecidedApplicationVersion,
) (*ApplicationReviewDecisionResult, error) {
	if review == nil || version == nil || !review.HasDecision() ||
		review.ApplicationID() != version.ApplicationID() || review.VersionID() != version.VersionID() ||
		string(review.Status()) != string(version.ReviewStatus()) ||
		review.Decision().DecidedBy() != version.UpdatedBy() ||
		!review.Decision().DecidedAt().Equal(version.UpdatedAt()) {
		return nil, NewInternalError(nil)
	}
	return &ApplicationReviewDecisionResult{review: *review, version: *version}, nil
}

func (result *ApplicationReviewDecisionResult) Review() *ApplicationReview {
	copy := result.review
	copy.snapshot = result.review.Snapshot()
	copy.decision = copyDecision(result.review.decision)
	return &copy
}

func (result *ApplicationReviewDecisionResult) Version() *DecidedApplicationVersion {
	copy := result.version
	return &copy
}
