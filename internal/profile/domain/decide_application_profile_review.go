package domain

import (
	"iwut-app-center/internal/shared"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ProfileReviewPermission = "app.profile.review"
const InitialProfileReviewPolicyVersion = "app-profile-review-v1"

func InitialProfileReviewChecks() []string {
	return []string{"content-policy-reviewed", "icon-content-reviewed"}
}

type ApplicationProfileReviewDecision struct {
	Outcome           ProfileReviewStatus
	PolicyVersion     string
	ConfirmedCheckIDs []string
	Reason            *string
	DecidedBy         shared.AuthID
	DecidedAt         time.Time
}

func (r *ApplicationProfileReview) Decision() *ApplicationProfileReviewDecision {
	if r.decision == nil {
		return nil
	}
	d := *r.decision
	d.ConfirmedCheckIDs = slices.Clone(d.ConfirmedCheckIDs)
	d.Reason = cloneString(d.Reason)
	return &d
}
func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	s := *v
	return &s
}

type ApplicationProfileDecisionResult struct {
	ProfileRevision                   *ApplicationProfileRevision
	Review                            *ApplicationProfileReview
	CurrentPublishedProfileRevisionID *ApplicationProfileRevisionID
}
type ProfileReviewPolicy struct {
	Version        string
	RequiredChecks []string
	Status         string
}

func ValidProfileReviewPolicyVersion(s string) bool {
	if len(s) < 1 || len(s) > 50 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
			return false
		}
	}
	return true
}
func ValidateProfileReviewReason(reason *string, required bool) error {
	if reason == nil {
		if required {
			return ErrInvalidProfileReviewReason
		}
		return nil
	}
	s := *reason
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 2000 {
		return ErrInvalidProfileReviewReason
	}
	runes := []rune(s)
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return ErrInvalidProfileReviewReason
	}
	for _, c := range runes {
		if unicode.Is(unicode.Cc, c) {
			return ErrInvalidProfileReviewReason
		}
	}
	return nil
}

type ApplicationProfileReviewState struct {
	ProfileReviewID   ApplicationProfileReviewID
	ApplicationID     shared.ApplicationID
	ProfileRevisionID ApplicationProfileRevisionID
	Attempt           int32
	SourceRevision    int64
	Status            ProfileReviewStatus
	DisplayName       string
	Description, Icon *string
	SubmittedBy       shared.AuthID
	SubmittedAt       time.Time
	Decision          *ApplicationProfileReviewDecision
}

// Decision candidates validate metadata without repairing or validating content.
// The APPROVE transition alone rechecks content; rejected history stays verbatim.
func RestoreProfileReviewDecisionCandidate(s ApplicationProfileReviewState) (*ApplicationProfileReview, error) {
	if !s.ProfileReviewID.IsValid() || !s.ApplicationID.IsValid() || !s.ProfileRevisionID.IsValid() || s.Attempt < 1 || s.SourceRevision < 1 || !s.SubmittedBy.IsValid() || s.SubmittedAt.IsZero() {
		return nil, ErrApplicationProfileReviewStateInconsistent
	}
	switch s.Status {
	case ProfileReviewStatusPending:
		if s.Decision != nil {
			return nil, ErrApplicationProfileReviewStateInconsistent
		}
	case ProfileReviewStatusApproved, ProfileReviewStatusRejected:
		d := s.Decision
		if d == nil || d.Outcome != s.Status || !ValidProfileReviewPolicyVersion(d.PolicyVersion) || !d.DecidedBy.IsValid() || d.DecidedAt.IsZero() || ValidateProfileReviewReason(d.Reason, s.Status == ProfileReviewStatusRejected) != nil {
			return nil, ErrApplicationProfileReviewStateInconsistent
		}
		if s.Status == ProfileReviewStatusApproved {
			if len(d.ConfirmedCheckIDs) == 0 || !slices.IsSorted(d.ConfirmedCheckIDs) {
				return nil, ErrApplicationProfileReviewStateInconsistent
			}
			for i, id := range d.ConfirmedCheckIDs {
				if !ValidProfileReviewPolicyVersion(id) || i > 0 && id == d.ConfirmedCheckIDs[i-1] {
					return nil, ErrApplicationProfileReviewStateInconsistent
				}
			}
			if ValidateStoredApplicationProfileContent(s.DisplayName, s.Description, s.Icon) != nil {
				return nil, ErrApplicationProfileReviewStateInconsistent
			}
		}
		if s.Status == ProfileReviewStatusRejected && len(d.ConfirmedCheckIDs) != 0 {
			return nil, ErrApplicationProfileReviewStateInconsistent
		}
	default:
		return nil, ErrApplicationProfileReviewStateInconsistent
	}
	var desc *ApplicationDescription
	var icon *ApplicationIcon
	if s.Description != nil {
		desc = &ApplicationDescription{*s.Description}
	}
	if s.Icon != nil {
		icon = &ApplicationIcon{*s.Icon}
	}
	r := &ApplicationProfileReview{s.ProfileReviewID, s.ApplicationID, s.ProfileRevisionID, s.Attempt, s.SourceRevision, ApplicationProfileReviewSnapshot{ApplicationDisplayName{s.DisplayName}, desc, icon}, s.SubmittedBy, s.SubmittedAt.UTC(), s.Status, s.Decision}
	r.decision = r.Decision()
	return r, nil
}
func (r *ApplicationProfileRevision) DecideReview(review *ApplicationProfileReview, reviewer, admin shared.AuthID, permissions []string, expected int64, outcome string, policy ProfileReviewPolicy, checks []string, reason *string, at time.Time) (*ApplicationProfileDecisionResult, error) {
	if !reviewer.IsValid() {
		return nil, ErrReviewerIdentityRequired
	}
	if !slices.Contains(permissions, ProfileReviewPermission) {
		return nil, ErrApplicationProfileReviewPermissionRequired
	}
	if r == nil || review == nil || !admin.IsValid() {
		return nil, ErrApplicationProfileReviewStateInconsistent
	}
	if reviewer == admin || reviewer == r.CreatedBy() || reviewer == review.SubmittedBy() {
		return nil, ErrApplicationProfileReviewConflictOfInterest
	}
	if review.status != ProfileReviewStatusPending {
		return nil, ErrApplicationProfileReviewAlreadyDecided
	}
	if r.reviewStatus != ReviewStatusSubmitted {
		return nil, ErrApplicationProfileReviewStateConflict
	}
	if r.revision != expected {
		return nil, ErrApplicationProfileRevisionConflict
	}
	sn := review.Snapshot()
	if review.applicationID != r.ApplicationID() || review.profileRevisionID != r.ProfileRevisionID() || review.sourceRevision == math.MaxInt64 || r.revision != review.sourceRevision+1 || r.DisplayName() != sn.DisplayName() || !equalDescription(r.Description(), sn.Description()) || !equalIcon(r.Icon(), sn.Icon()) {
		return nil, ErrApplicationProfileReviewStateInconsistent
	}
	if policy.Status != "ACTIVE" || !ValidProfileReviewPolicyVersion(policy.Version) {
		return nil, ErrProfileReviewPolicyUnavailable
	}
	if outcome != "APPROVE" && outcome != "REJECT" {
		return nil, ErrInvalidApplicationProfileReviewDecision
	}
	if err := ValidateProfileReviewReason(reason, outcome == "REJECT"); err != nil {
		return nil, err
	}
	sorted := slices.Clone(checks)
	slices.Sort(sorted)
	if outcome == "APPROVE" {
		required := slices.Clone(policy.RequiredChecks)
		slices.Sort(required)
		if len(sorted) == 0 || !slices.Equal(sorted, required) || len(slices.Compact(slices.Clone(sorted))) != len(sorted) {
			return nil, ErrProfileReviewChecksIncomplete
		}
		var d, i *string
		if sn.Description() != nil {
			v := sn.Description().String()
			d = &v
		}
		if sn.Icon() != nil {
			v := sn.Icon().String()
			i = &v
		}
		if err := ValidateStoredApplicationProfileContent(sn.DisplayName().String(), d, i); err != nil {
			return nil, err
		}
	} else if len(checks) != 0 {
		return nil, ErrProfileReviewChecksIncomplete
	}
	if r.revision == math.MaxInt64 || at.IsZero() {
		return nil, ErrApplicationProfileReviewStateInconsistent
	}
	status := ProfileReviewStatusApproved
	if outcome == "REJECT" {
		status = ProfileReviewStatusRejected
	}
	revision := *r
	revision.reviewStatus = ReviewStatus(status)
	revision.revision++
	revision.updatedBy = reviewer
	revision.updatedAt = at.UTC()
	updated := *review
	updated.status = status
	updated.decision = &ApplicationProfileReviewDecision{status, policy.Version, sorted, cloneString(reason), reviewer, at.UTC()}
	return &ApplicationProfileDecisionResult{ProfileRevision: &revision, Review: &updated}, nil
}
