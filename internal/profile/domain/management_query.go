package domain

import "time"

const ProfileManagementResponseBudget = 2 * 1024 * 1024

type ManagedProfileState struct {
	WorkingProfileRevisionID, CurrentPublishedProfileRevisionID *string
}

type ManagedProfileDecision struct {
	Outcome, PolicyVersion string
	Reason                 *string
	DecidedAt              time.Time
}

type ManagedProfileReview struct {
	ProfileReviewID string
	Attempt         int32
	Status          string
	SourceRevision  int64
	SubmittedAt     time.Time
	Decision        *ManagedProfileDecision
}

type ManagedProfileRevision struct {
	ApplicationID, ProfileRevisionID             string
	Sequence                                     int32
	ReviewStatus                                 string
	Revision                                     int64
	DisplayName                                  string
	Description, Icon                            *string
	CreatedBy                                    string
	CreatedAt, UpdatedAt                         time.Time
	IsCurrentWorkingRevision, IsCurrentPublished bool
	LatestReview                                 *ManagedProfileReview
}

type ManagedProfilePage struct {
	Items         []ManagedProfileRevision
	ProfileState  ManagedProfileState
	NextPageToken string
	AsOf          time.Time
}

type ManagedProfileDetail struct {
	Profile             ManagedProfileRevision
	ProfileState        ManagedProfileState
	Reviews             []ManagedProfileReview
	NextReviewPageToken string
	AsOf                time.Time
}

const (
	ErrorCodeProfileManagementNotFound          ErrorCode = "ApplicationProfileManagementNotFound"
	ErrorCodeInvalidProfileManagementQuery      ErrorCode = "InvalidApplicationProfileManagementQuery"
	ErrorCodeInvalidProfileManagementPageToken  ErrorCode = "InvalidApplicationProfileManagementPageToken"
	ErrorCodeProfileManagementStateInconsistent ErrorCode = "ApplicationProfileManagementStateInconsistent"
	ErrorCodeProfileManagementUnavailable       ErrorCode = "ApplicationProfileManagementUnavailable"
	ErrorCodeProfileManagementResponseTooLarge  ErrorCode = "ApplicationProfileManagementResponseTooLarge"
	ErrorCodeUserIdentityRequired               ErrorCode = "UserIdentityRequired"
)

var (
	ErrProfileManagementNotFound          = newError(ErrorCategoryNotFound, ErrorCodeProfileManagementNotFound, "application profile management resource not found", nil)
	ErrInvalidProfileManagementQuery      = newError(ErrorCategoryValidation, ErrorCodeInvalidProfileManagementQuery, "application profile management query is invalid", nil)
	ErrInvalidProfileManagementPageToken  = newError(ErrorCategoryValidation, ErrorCodeInvalidProfileManagementPageToken, "application profile management page token is invalid", nil)
	ErrProfileManagementStateInconsistent = newError(ErrorCategoryInternal, ErrorCodeProfileManagementStateInconsistent, "application profile management state is inconsistent", nil)
	ErrProfileManagementUnavailable       = NewProfileManagementUnavailableError(nil)
	ErrProfileManagementResponseTooLarge  = newError(ErrorCategory("ResourceExhausted"), ErrorCodeProfileManagementResponseTooLarge, "application profile management response is too large", nil)
	ErrUserIdentityRequired               = newError(ErrorCategoryAuthentication, ErrorCodeUserIdentityRequired, "user identity is required", nil)
)

func NewProfileManagementUnavailableError(cause error) *Error {
	return newError(ErrorCategory("DependencyUnavailable"), ErrorCodeProfileManagementUnavailable, "application profile management is unavailable", cause)
}
