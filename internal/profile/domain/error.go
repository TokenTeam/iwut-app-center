package domain

import "errors"

type ErrorCategory string

const (
	ErrorCategoryValidation     ErrorCategory = "Validation"
	ErrorCategoryAuthentication ErrorCategory = "Authentication"
	ErrorCategoryAuthorization  ErrorCategory = "Authorization"
	ErrorCategoryNotFound       ErrorCategory = "NotFound"
	ErrorCategoryConflict       ErrorCategory = "Conflict"
	ErrorCategoryInternal       ErrorCategory = "Internal"
)

type ErrorCode string

const (
	ErrorCodeInvalidApplicationID                        ErrorCode = "InvalidApplicationId"
	ErrorCodeDeveloperIdentityRequired                   ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired                   ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidApplicationDisplayName               ErrorCode = "InvalidApplicationDisplayName"
	ErrorCodeInvalidApplicationDescription               ErrorCode = "InvalidApplicationDescription"
	ErrorCodeInvalidApplicationIcon                      ErrorCode = "InvalidApplicationIcon"
	ErrorCodeApplicationNotFound                         ErrorCode = "ApplicationNotFound"
	ErrorCodeApplicationAdminRequired                    ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationProfileWorkRevisionAlreadyExists ErrorCode = "ApplicationProfileWorkRevisionAlreadyExists"
	ErrorCodeApplicationProfileStateInconsistent         ErrorCode = "ApplicationProfileStateInconsistent"
	ErrorCodeInvalidApplicationProfileRevisionID         ErrorCode = "InvalidApplicationProfileRevisionId"
	ErrorCodeApplicationProfileExpectedRevisionRequired  ErrorCode = "ApplicationProfileExpectedRevisionRequired"
	ErrorCodeApplicationProfileRevisionNotFound          ErrorCode = "ApplicationProfileRevisionNotFound"
	ErrorCodeApplicationProfileRevisionNotDraft          ErrorCode = "ApplicationProfileRevisionNotDraft"
	ErrorCodeApplicationProfileRevisionConflict          ErrorCode = "ApplicationProfileRevisionConflict"
	ErrorCodeInvalidApplicationProfileReviewSubmission   ErrorCode = "InvalidApplicationProfileReviewSubmission"
	ErrorCodeInvalidApplicationProfileContent            ErrorCode = "InvalidApplicationProfileContent"
	ErrorCodeInternal                                    ErrorCode = "Internal"
)

type Error struct {
	category ErrorCategory
	code     ErrorCode
	message  string
	cause    error
}

func newError(category ErrorCategory, code ErrorCode, message string, cause error) *Error {
	return &Error{category, code, message, cause}
}
func (e *Error) Error() string           { return e.message }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}
func NewInternalError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", cause)
}
func NewApplicationProfileStateInconsistentError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeApplicationProfileStateInconsistent, "application profile state is inconsistent", cause)
}

var (
	ErrInvalidApplicationID                        = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationID, "application ID is invalid", nil)
	ErrDeveloperIdentityRequired                   = newError(ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil)
	ErrDeveloperApprovalRequired                   = newError(ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil)
	ErrInvalidApplicationDisplayName               = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationDisplayName, "application display name is invalid", nil)
	ErrInvalidApplicationDescription               = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationDescription, "application description is invalid", nil)
	ErrInvalidApplicationIcon                      = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationIcon, "application icon is invalid", nil)
	ErrApplicationNotFound                         = newError(ErrorCategoryNotFound, ErrorCodeApplicationNotFound, "application not found", nil)
	ErrApplicationAdminRequired                    = newError(ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil)
	ErrApplicationProfileWorkRevisionAlreadyExists = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileWorkRevisionAlreadyExists, "application profile work revision already exists", nil)
	ErrApplicationProfileStateInconsistent         = NewApplicationProfileStateInconsistentError(nil)
	ErrInvalidApplicationProfileRevisionID         = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationProfileRevisionID, "application profile revision ID is invalid", nil)
	ErrApplicationProfileExpectedRevisionRequired  = newError(ErrorCategoryValidation, ErrorCodeApplicationProfileExpectedRevisionRequired, "application profile expected revision is required", nil)
	ErrApplicationProfileRevisionNotFound          = newError(ErrorCategoryNotFound, ErrorCodeApplicationProfileRevisionNotFound, "application profile revision not found", nil)
	ErrApplicationProfileRevisionNotDraft          = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileRevisionNotDraft, "application profile revision is not draft", nil)
	ErrApplicationProfileRevisionConflict          = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileRevisionConflict, "application profile revision conflict", nil)
	ErrInvalidApplicationProfileReviewSubmission   = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationProfileReviewSubmission, "application profile review submission is invalid", nil)
	ErrInvalidApplicationProfileContent            = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationProfileContent, "application profile content is invalid", nil)
	ErrInternal                                    = NewInternalError(nil)
)

const (
	ErrorCodeReviewerIdentityRequired                   ErrorCode = "ReviewerIdentityRequired"
	ErrorCodeApplicationProfileReviewPermissionRequired ErrorCode = "ApplicationProfileReviewPermissionRequired"
	ErrorCodeApplicationProfileReviewConflictOfInterest ErrorCode = "ApplicationProfileReviewConflictOfInterest"
	ErrorCodeInvalidApplicationProfileReviewDecision    ErrorCode = "InvalidApplicationProfileReviewDecision"
	ErrorCodeApplicationProfileReviewNotFound           ErrorCode = "ApplicationProfileReviewNotFound"
	ErrorCodeApplicationProfileReviewAlreadyDecided     ErrorCode = "ApplicationProfileReviewAlreadyDecided"
	ErrorCodeApplicationProfileReviewStateConflict      ErrorCode = "ApplicationProfileReviewStateConflict"
	ErrorCodeApplicationProfilePublicationConflict      ErrorCode = "ApplicationProfilePublicationConflict"
	ErrorCodeApplicationProfileReviewStateInconsistent  ErrorCode = "ApplicationProfileReviewStateInconsistent"
	ErrorCodeProfileReviewPolicyUnavailable             ErrorCode = "ProfileReviewPolicyUnavailable"
	ErrorCodeProfileReviewChecksIncomplete              ErrorCode = "ProfileReviewChecksIncomplete"
	ErrorCodeInvalidProfileReviewReason                 ErrorCode = "InvalidProfileReviewReason"
)

var (
	ErrReviewerIdentityRequired                   = newError(ErrorCategoryAuthentication, ErrorCodeReviewerIdentityRequired, "ReviewerIdentityRequired", nil)
	ErrApplicationProfileReviewPermissionRequired = newError(ErrorCategoryAuthorization, ErrorCodeApplicationProfileReviewPermissionRequired, "ApplicationProfileReviewPermissionRequired", nil)
	ErrApplicationProfileReviewConflictOfInterest = newError(ErrorCategoryAuthorization, ErrorCodeApplicationProfileReviewConflictOfInterest, "ApplicationProfileReviewConflictOfInterest", nil)
	ErrInvalidApplicationProfileReviewDecision    = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationProfileReviewDecision, "InvalidApplicationProfileReviewDecision", nil)
	ErrApplicationProfileReviewNotFound           = newError(ErrorCategoryNotFound, ErrorCodeApplicationProfileReviewNotFound, "ApplicationProfileReviewNotFound", nil)
	ErrApplicationProfileReviewAlreadyDecided     = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileReviewAlreadyDecided, "ApplicationProfileReviewAlreadyDecided", nil)
	ErrApplicationProfileReviewStateConflict      = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileReviewStateConflict, "ApplicationProfileReviewStateConflict", nil)
	ErrApplicationProfilePublicationConflict      = newError(ErrorCategoryConflict, ErrorCodeApplicationProfilePublicationConflict, "ApplicationProfilePublicationConflict", nil)
	ErrApplicationProfileReviewStateInconsistent  = newError(ErrorCategoryInternal, ErrorCodeApplicationProfileReviewStateInconsistent, "ApplicationProfileReviewStateInconsistent", nil)
	ErrProfileReviewPolicyUnavailable             = newError(ErrorCategoryConflict, ErrorCodeProfileReviewPolicyUnavailable, "ProfileReviewPolicyUnavailable", nil)
	ErrProfileReviewChecksIncomplete              = newError(ErrorCategoryValidation, ErrorCodeProfileReviewChecksIncomplete, "ProfileReviewChecksIncomplete", nil)
	ErrInvalidProfileReviewReason                 = newError(ErrorCategoryValidation, ErrorCodeInvalidProfileReviewReason, "InvalidProfileReviewReason", nil)
)
