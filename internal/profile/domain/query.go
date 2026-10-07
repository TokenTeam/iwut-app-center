package domain

import "time"

type ProfileReviewConflict string

const (
	ProfileConflictCurrentAdmin    ProfileReviewConflict = "CURRENT_ADMIN"
	ProfileConflictRevisionCreator ProfileReviewConflict = "REVISION_CREATOR"
	ProfileConflictReviewSubmitter ProfileReviewConflict = "REVIEW_SUBMITTER"
)

type ProfileDecisionEligibility struct {
	Eligible  bool
	Conflicts []ProfileReviewConflict
}
type PendingProfileReviewSummary struct {
	ApplicationID, ApplicationName, ProfileRevisionID, DisplayName, ProfileReviewID string
	Sequence, Attempt                                                               int32
	SubmittedAt                                                                     time.Time
	DecisionEligibility                                                             ProfileDecisionEligibility
}
type PendingProfileReviewPage struct {
	Items         []PendingProfileReviewSummary
	NextPageToken string
	AsOf          time.Time
}
type ProfileReviewApplicationContext struct{ ApplicationID, Name, AdminID, LifecycleStatus, PlatformAvailabilityStatus string }
type ProfileRevisionReviewContext struct {
	ProfileRevisionID string
	Sequence          int32
	ReviewStatus      string
	Revision          int64
	CreatedBy         string
}
type ProfileReviewPolicyContext struct {
	Version          string
	RequiredCheckIDs []string
}
type ProfileReviewDetail struct {
	Application         ProfileReviewApplicationContext
	ProfileRevision     ProfileRevisionReviewContext
	Review              *ApplicationProfileReview
	CurrentPolicy       ProfileReviewPolicyContext
	DecisionEligibility ProfileDecisionEligibility
	AsOf                time.Time
}
