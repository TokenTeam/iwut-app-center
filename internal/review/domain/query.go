package domain

import (
	"iwut-app-center/internal/shared"
	"time"
)

type ReviewConflict string

const (
	ConflictCurrentAdmin    ReviewConflict = "CURRENT_ADMIN"
	ConflictVersionCreator  ReviewConflict = "VERSION_CREATOR"
	ConflictReviewSubmitter ReviewConflict = "REVIEW_SUBMITTER"
)

type DecisionEligibility struct {
	Eligible  bool
	Conflicts []ReviewConflict
}
type PendingReviewSummary struct {
	ApplicationID, ApplicationName, VersionID, VersionLabel, ReviewID, SubmittedBy string
	Attempt                                                                        int32
	SubmittedAt                                                                    time.Time
	DecisionEligibility                                                            DecisionEligibility
}
type PendingReviewPage struct {
	Items         []PendingReviewSummary
	NextPageToken string
	AsOf          time.Time
}
type ReviewApplicationContext struct{ ApplicationID, Name, AdminID, LifecycleStatus, PlatformAvailabilityStatus string }
type ReviewVersionContext struct {
	VersionID    string
	Sequence     int32
	ReviewStatus string
	Revision     int64
	CreatedBy    string
}
type ReviewPolicyContext struct {
	Version                  string
	RequiredCheckIDs         []string
	RequiredCheckDefinitions []ReviewCheckDefinition
}
type ReviewDetail struct {
	ScriptDependencies  []shared.ScriptDependencySummary
	Application         ReviewApplicationContext
	Version             ReviewVersionContext
	Review              *ApplicationReview
	ReviewPolicy        ReviewPolicyContext
	DecisionEligibility DecisionEligibility
	AsOf                time.Time
}

type ApplicationReviewScriptDetail struct {
	Dependency    shared.ScriptDependencySummary
	Text          string
	PolicyVersion string
	Checks        []ApplicationReviewScriptCheck
}

type ApplicationReviewScriptCheck struct {
	ID, Title, Description string
	Mandatory              bool
}
