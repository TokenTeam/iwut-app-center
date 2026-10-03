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
	ErrorCodeDeveloperIdentityRequired              ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired              ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidRpcApiMajor                     ErrorCode = "InvalidRpcApiMajor"
	ErrorCodeInvalidApplicationVersionId            ErrorCode = "InvalidApplicationVersionId"
	ErrorCodeInvalidApplicationPublicationRevision  ErrorCode = "InvalidApplicationPublicationRevision"
	ErrorCodeApplicationVersionNotFound             ErrorCode = "ApplicationVersionNotFound"
	ErrorCodeApplicationAdminRequired               ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationVersionNotApproved          ErrorCode = "ApplicationVersionNotApproved"
	ErrorCodeApplicationReviewStateInconsistent     ErrorCode = "ApplicationReviewStateInconsistent"
	ErrorCodeApplicationVersionRpcApiIncompatible   ErrorCode = "ApplicationVersionRpcApiIncompatible"
	ErrorCodeApplicationPublicationAlreadyExists    ErrorCode = "ApplicationPublicationAlreadyExists"
	ErrorCodeApplicationPublicationNotFound         ErrorCode = "ApplicationPublicationNotFound"
	ErrorCodeApplicationPublicationRevisionConflict ErrorCode = "ApplicationPublicationRevisionConflict"
	ErrorCodeInvalidApplicationScope                ErrorCode = "InvalidApplicationScope"
	ErrorCodeScopeCatalogUnavailable                ErrorCode = "ScopeCatalogUnavailable"
	ErrorCodeApplicationLaunchURLNotReviewable      ErrorCode = "ApplicationLaunchUrlNotReviewable"
	ErrorCodeLaunchURLInspectionUnavailable         ErrorCode = "LaunchUrlInspectionUnavailable"
	ErrorCodeOAuthClientRegistrationRequired        ErrorCode = "OAuthClientRegistrationRequired"
	ErrorCodeApplicationProfileRequired             ErrorCode = "ApplicationProfileRequired"
	ErrorCodeApplicationProfileStateInconsistent    ErrorCode = "ApplicationProfileStateInconsistent"
	ErrorCodeInternal                               ErrorCode = "Internal"
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

func NewApplicationProfileStateInconsistentError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeApplicationProfileStateInconsistent, "application profile state is inconsistent", cause)
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
	ErrDeveloperIdentityRequired              = newError(ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil)
	ErrDeveloperApprovalRequired              = newError(ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil)
	ErrInvalidRpcApiMajor                     = newError(ErrorCategoryValidation, ErrorCodeInvalidRpcApiMajor, "RPC API major is invalid", nil)
	ErrInvalidApplicationVersionId            = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationVersionId, "application version ID is invalid", nil)
	ErrInvalidApplicationPublicationRevision  = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationPublicationRevision, "publication revision is invalid", nil)
	ErrApplicationVersionNotFound             = newError(ErrorCategoryNotFound, ErrorCodeApplicationVersionNotFound, "application version not found", nil)
	ErrApplicationAdminRequired               = newError(ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil)
	ErrApplicationVersionNotApproved          = newError(ErrorCategoryConflict, ErrorCodeApplicationVersionNotApproved, "application version is not approved", nil)
	ErrApplicationReviewStateInconsistent     = newError(ErrorCategoryConflict, ErrorCodeApplicationReviewStateInconsistent, "application review state is inconsistent", nil)
	ErrApplicationVersionRpcApiIncompatible   = newError(ErrorCategoryValidation, ErrorCodeApplicationVersionRpcApiIncompatible, "application version RPC API range is incompatible", nil)
	ErrApplicationPublicationAlreadyExists    = newError(ErrorCategoryConflict, ErrorCodeApplicationPublicationAlreadyExists, "application publication already exists", nil)
	ErrApplicationPublicationNotFound         = newError(ErrorCategoryNotFound, ErrorCodeApplicationPublicationNotFound, "application publication not found", nil)
	ErrApplicationPublicationRevisionConflict = newError(ErrorCategoryConflict, ErrorCodeApplicationPublicationRevisionConflict, "application publication revision conflicts", nil)
	ErrInvalidApplicationScope                = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationScope, "application scope request is invalid", nil)
	ErrScopeCatalogUnavailable                = newError(ErrorCategoryDependencyUnavailable, ErrorCodeScopeCatalogUnavailable, "scope catalog is unavailable", nil)
	ErrApplicationLaunchURLNotReviewable      = newError(ErrorCategoryValidation, ErrorCodeApplicationLaunchURLNotReviewable, "application launch URL is not reviewable", nil)
	ErrLaunchURLInspectionUnavailable         = newError(ErrorCategoryDependencyUnavailable, ErrorCodeLaunchURLInspectionUnavailable, "launch URL inspection is unavailable", nil)
	ErrOAuthClientRegistrationRequired        = newError(ErrorCategoryConflict, ErrorCodeOAuthClientRegistrationRequired, "OAuth client registration is required", nil)
	ErrApplicationProfileRequired             = newError(ErrorCategoryConflict, ErrorCodeApplicationProfileRequired, "application profile is required", nil)
	ErrApplicationProfileStateInconsistent    = newError(ErrorCategoryInternal, ErrorCodeApplicationProfileStateInconsistent, "application profile state is inconsistent", nil)
	ErrInternal                               = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", nil)
)
