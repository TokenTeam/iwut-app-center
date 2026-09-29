package domain

import "errors"

type ErrorCategory string

const (
	ErrorCategoryValidation            ErrorCategory = "Validation"
	ErrorCategoryAuthentication        ErrorCategory = "Authentication"
	ErrorCategoryAuthorization         ErrorCategory = "Authorization"
	ErrorCategoryNotFound              ErrorCategory = "NotFound"
	ErrorCategoryConflict              ErrorCategory = "Conflict"
	ErrorCategoryDependencyUnavailable ErrorCategory = "DependencyUnavailable"
	ErrorCategoryInternal              ErrorCategory = "Internal"
)

type ErrorCode string

const (
	ErrorCodeDeveloperIdentityRequired             ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired             ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeApplicationVersionRevisionRequired    ErrorCode = "ApplicationVersionRevisionRequired"
	ErrorCodeApplicationVersionNotFound            ErrorCode = "ApplicationVersionNotFound"
	ErrorCodeApplicationAdminRequired              ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationVersionNotDraft            ErrorCode = "ApplicationVersionNotDraft"
	ErrorCodeApplicationVersionRevisionConflict    ErrorCode = "ApplicationVersionRevisionConflict"
	ErrorCodeApplicationLaunchURLNotReviewable     ErrorCode = "ApplicationLaunchUrlNotReviewable"
	ErrorCodeInvalidOAuthRedirectConfiguration     ErrorCode = "InvalidOAuthRedirectConfiguration"
	ErrorCodeLaunchURLInspectionUnavailable        ErrorCode = "LaunchUrlInspectionUnavailable"
	ErrorCodeInvalidApplicationScope               ErrorCode = "InvalidApplicationScope"
	ErrorCodeScopeCatalogUnavailable               ErrorCode = "ScopeCatalogUnavailable"
	ErrorCodeReviewerIdentityRequired              ErrorCode = "ReviewerIdentityRequired"
	ErrorCodeApplicationReviewPermissionRequired   ErrorCode = "ApplicationReviewPermissionRequired"
	ErrorCodeApplicationReviewNotFound             ErrorCode = "ApplicationReviewNotFound"
	ErrorCodeApplicationReviewAlreadyDecided       ErrorCode = "ApplicationReviewAlreadyDecided"
	ErrorCodeApplicationReviewNotLatest            ErrorCode = "ApplicationReviewNotLatest"
	ErrorCodeApplicationReviewAlreadyRestored      ErrorCode = "ApplicationReviewAlreadyRestored"
	ErrorCodeApplicationVersionNotRejected         ErrorCode = "ApplicationVersionNotRejected"
	ErrorCodeApplicationReviewStateInconsistent    ErrorCode = "ApplicationReviewStateInconsistent"
	ErrorCodeApplicationReviewConflictOfInterest   ErrorCode = "ApplicationReviewConflictOfInterest"
	ErrorCodeInvalidApplicationReviewOutcome       ErrorCode = "InvalidApplicationReviewOutcome"
	ErrorCodeInvalidApplicationReviewPolicyVersion ErrorCode = "InvalidApplicationReviewPolicyVersion"
	ErrorCodeApplicationReviewPolicyChanged        ErrorCode = "ApplicationReviewPolicyChanged"
	ErrorCodeApplicationReviewChecksIncomplete     ErrorCode = "ApplicationReviewChecksIncomplete"
	ErrorCodeInvalidApplicationReviewChecks        ErrorCode = "InvalidApplicationReviewChecks"
	ErrorCodeInvalidApplicationReviewReason        ErrorCode = "InvalidApplicationReviewReason"
	ErrorCodeDeveloperStatusUnavailable            ErrorCode = "DeveloperStatusUnavailable"
	ErrorCodeSystemPrincipalUnavailable            ErrorCode = "SystemPrincipalUnavailable"
	ErrorCodeInternal                              ErrorCode = "Internal"
)

type Error struct {
	category ErrorCategory
	code     ErrorCode
	message  string
	cause    error
}

func newError(category ErrorCategory, code ErrorCode, message string, cause error) *Error {
	return &Error{category: category, code: code, message: message, cause: cause}
}

func NewInternalError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", cause)
}

func NewLaunchURLInspectionUnavailableError(cause error) *Error {
	return newError(ErrorCategoryDependencyUnavailable, ErrorCodeLaunchURLInspectionUnavailable, "launch URL inspection is unavailable", cause)
}

func NewScopeCatalogUnavailableError(cause error) *Error {
	return newError(ErrorCategoryDependencyUnavailable, ErrorCodeScopeCatalogUnavailable, "scope catalog is unavailable", cause)
}

func NewDeveloperStatusUnavailableError(cause error) *Error {
	return newError(ErrorCategoryDependencyUnavailable, ErrorCodeDeveloperStatusUnavailable, "developer status is unavailable", cause)
}

func NewSystemPrincipalUnavailableError(cause error) *Error {
	return newError(ErrorCategoryDependencyUnavailable, ErrorCodeSystemPrincipalUnavailable, "system principal is unavailable", cause)
}

func (e *Error) Error() string           { return e.message }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}

var (
	ErrDeveloperIdentityRequired             = newError(ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil)
	ErrDeveloperApprovalRequired             = newError(ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil)
	ErrApplicationVersionRevisionRequired    = newError(ErrorCategoryValidation, ErrorCodeApplicationVersionRevisionRequired, "application version revision is required", nil)
	ErrApplicationVersionNotFound            = newError(ErrorCategoryNotFound, ErrorCodeApplicationVersionNotFound, "application version not found", nil)
	ErrApplicationAdminRequired              = newError(ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil)
	ErrApplicationVersionNotDraft            = newError(ErrorCategoryConflict, ErrorCodeApplicationVersionNotDraft, "application version is not a draft", nil)
	ErrApplicationVersionRevisionConflict    = newError(ErrorCategoryConflict, ErrorCodeApplicationVersionRevisionConflict, "application version revision conflicts", nil)
	ErrApplicationLaunchURLNotReviewable     = newError(ErrorCategoryValidation, ErrorCodeApplicationLaunchURLNotReviewable, "application launch URL is not reviewable", nil)
	ErrInvalidOAuthRedirectConfiguration     = newError(ErrorCategoryValidation, ErrorCodeInvalidOAuthRedirectConfiguration, "OAuth redirect configuration is invalid", nil)
	ErrLaunchURLInspectionUnavailable        = newError(ErrorCategoryDependencyUnavailable, ErrorCodeLaunchURLInspectionUnavailable, "launch URL inspection is unavailable", nil)
	ErrInvalidApplicationScope               = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationScope, "application scope request is invalid", nil)
	ErrScopeCatalogUnavailable               = newError(ErrorCategoryDependencyUnavailable, ErrorCodeScopeCatalogUnavailable, "scope catalog is unavailable", nil)
	ErrReviewerIdentityRequired              = newError(ErrorCategoryAuthentication, ErrorCodeReviewerIdentityRequired, "reviewer identity is required", nil)
	ErrApplicationReviewPermissionRequired   = newError(ErrorCategoryAuthorization, ErrorCodeApplicationReviewPermissionRequired, "application version review permission is required", nil)
	ErrApplicationReviewNotFound             = newError(ErrorCategoryNotFound, ErrorCodeApplicationReviewNotFound, "application review not found", nil)
	ErrApplicationReviewAlreadyDecided       = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewAlreadyDecided, "application review is already decided", nil)
	ErrApplicationReviewNotLatest            = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewNotLatest, "application review is not the latest attempt", nil)
	ErrApplicationReviewAlreadyRestored      = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewAlreadyRestored, "application review is already restored", nil)
	ErrApplicationVersionNotRejected         = newError(ErrorCategoryConflict, ErrorCodeApplicationVersionNotRejected, "application version is not rejected", nil)
	ErrApplicationReviewStateInconsistent    = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewStateInconsistent, "application review state is inconsistent", nil)
	ErrApplicationReviewConflictOfInterest   = newError(ErrorCategoryAuthorization, ErrorCodeApplicationReviewConflictOfInterest, "reviewer has a conflict of interest", nil)
	ErrInvalidApplicationReviewOutcome       = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationReviewOutcome, "application review outcome is invalid", nil)
	ErrInvalidApplicationReviewPolicyVersion = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationReviewPolicyVersion, "application review policy version is invalid", nil)
	ErrApplicationReviewPolicyChanged        = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewPolicyChanged, "application review policy is no longer usable", nil)
	ErrApplicationReviewChecksIncomplete     = newError(ErrorCategoryValidation, ErrorCodeApplicationReviewChecksIncomplete, "application review confirmation is incomplete", nil)
	ErrInvalidApplicationReviewChecks        = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationReviewChecks, "application review confirmation is invalid", nil)
	ErrInvalidApplicationReviewReason        = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationReviewReason, "application review reason is invalid", nil)
	ErrDeveloperStatusUnavailable            = newError(ErrorCategoryDependencyUnavailable, ErrorCodeDeveloperStatusUnavailable, "developer status is unavailable", nil)
	ErrSystemPrincipalUnavailable            = newError(ErrorCategoryDependencyUnavailable, ErrorCodeSystemPrincipalUnavailable, "system principal is unavailable", nil)
	ErrInternal                              = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", nil)
)
