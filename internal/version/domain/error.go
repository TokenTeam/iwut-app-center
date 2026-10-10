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
	ErrorCodeInvalidApplicationID                          ErrorCode = "InvalidApplicationId"
	ErrorCodeDeveloperIdentityRequired                     ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired                     ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidVersionLabel                           ErrorCode = "InvalidVersionLabel"
	ErrorCodeInvalidApplicationLaunchURL                   ErrorCode = "InvalidApplicationLaunchUrl"
	ErrorCodeInvalidRPCApiRange                            ErrorCode = "InvalidRpcApiRange"
	ErrorCodeInvalidRequiredCapability                     ErrorCode = "InvalidRequiredCapability"
	ErrorCodeInvalidApplicationScope                       ErrorCode = "InvalidApplicationScope"
	ErrorCodeInvalidOAuthRedirectConfiguration             ErrorCode = "InvalidOAuthRedirectConfiguration"
	ErrorCodeScopeCatalogUnavailable                       ErrorCode = "ScopeCatalogUnavailable"
	ErrorCodeApplicationNotFound                           ErrorCode = "ApplicationNotFound"
	ErrorCodeApplicationAdminRequired                      ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationVersionRevisionRequired            ErrorCode = "ApplicationVersionRevisionRequired"
	ErrorCodeApplicationVersionNotFound                    ErrorCode = "ApplicationVersionNotFound"
	ErrorCodeApplicationVersionNotDraft                    ErrorCode = "ApplicationVersionNotDraft"
	ErrorCodeApplicationVersionNotRejected                 ErrorCode = "ApplicationVersionNotRejected"
	ErrorCodeApplicationVersionRevisionConflict            ErrorCode = "ApplicationVersionRevisionConflict"
	ErrorCodeApplicationVersionLabelAlreadyExists          ErrorCode = "ApplicationVersionLabelAlreadyExists"
	ErrorCodeUserIdentityRequired                          ErrorCode = "UserIdentityRequired"
	ErrorCodeInvalidApplicationVersionQuery                ErrorCode = "InvalidApplicationVersionQuery"
	ErrorCodeInvalidApplicationVersionPageToken            ErrorCode = "InvalidApplicationVersionPageToken"
	ErrorCodeApplicationVersionManagementNotFound          ErrorCode = "ApplicationVersionManagementNotFound"
	ErrorCodeApplicationVersionManagementStateInconsistent ErrorCode = "ApplicationVersionManagementStateInconsistent"
	ErrorCodeApplicationVersionManagementUnavailable       ErrorCode = "ApplicationVersionManagementUnavailable"
	ErrorCodeInternal                                      ErrorCode = "Internal"
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

func NewScopeCatalogUnavailableError(cause error) *Error {
	return newError(
		ErrorCategoryDependencyUnavailable,
		ErrorCodeScopeCatalogUnavailable,
		"scope catalog is unavailable",
		cause,
	)
}

func NewApplicationVersionManagementUnavailableError(cause error) *Error {
	return newError(ErrorCategoryDependencyUnavailable, ErrorCodeApplicationVersionManagementUnavailable, "application version management query is unavailable", cause)
}

func (e *Error) Error() string { return e.message }
func (e *Error) Unwrap() error { return e.cause }
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }

var (
	ErrInvalidApplicationID = newError(
		ErrorCategoryValidation, ErrorCodeInvalidApplicationID, "application ID is invalid", nil,
	)
	ErrDeveloperIdentityRequired = newError(
		ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil,
	)
	ErrDeveloperApprovalRequired = newError(
		ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil,
	)
	ErrInvalidVersionLabel = newError(
		ErrorCategoryValidation, ErrorCodeInvalidVersionLabel, "version label is invalid", nil,
	)
	ErrInvalidApplicationLaunchURL = newError(
		ErrorCategoryValidation, ErrorCodeInvalidApplicationLaunchURL, "application launch URL is invalid", nil,
	)
	ErrInvalidRPCApiRange = newError(
		ErrorCategoryValidation, ErrorCodeInvalidRPCApiRange, "RPC API range is invalid", nil,
	)
	ErrInvalidRequiredCapability = newError(
		ErrorCategoryValidation, ErrorCodeInvalidRequiredCapability, "required capability is invalid", nil,
	)
	ErrInvalidApplicationScope = newError(
		ErrorCategoryValidation, ErrorCodeInvalidApplicationScope, "application scope request is invalid", nil,
	)
	ErrInvalidOAuthRedirectConfiguration = newError(
		ErrorCategoryValidation, ErrorCodeInvalidOAuthRedirectConfiguration, "OAuth redirect configuration is invalid", nil,
	)
	ErrScopeCatalogUnavailable = newError(
		ErrorCategoryDependencyUnavailable, ErrorCodeScopeCatalogUnavailable, "scope catalog is unavailable", nil,
	)
	ErrApplicationNotFound = newError(
		ErrorCategoryNotFound, ErrorCodeApplicationNotFound, "application not found", nil,
	)
	ErrApplicationAdminRequired = newError(
		ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil,
	)
	ErrApplicationVersionRevisionRequired = newError(
		ErrorCategoryValidation, ErrorCodeApplicationVersionRevisionRequired, "application version revision is required", nil,
	)
	ErrApplicationVersionNotFound = newError(
		ErrorCategoryNotFound, ErrorCodeApplicationVersionNotFound, "application version not found", nil,
	)
	ErrApplicationVersionNotDraft = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionNotDraft, "application version is not a draft", nil,
	)
	ErrApplicationVersionNotRejected = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionNotRejected, "application version is not rejected", nil,
	)
	ErrApplicationVersionRevisionConflict = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionRevisionConflict, "application version revision conflicts", nil,
	)
	ErrApplicationVersionLabelAlreadyExists = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionLabelAlreadyExists, "application version label already exists", nil,
	)
	ErrUserIdentityRequired                          = newError(ErrorCategoryAuthentication, ErrorCodeUserIdentityRequired, "user identity is required", nil)
	ErrInvalidApplicationVersionQuery                = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationVersionQuery, "application version query is invalid", nil)
	ErrInvalidApplicationVersionPageToken            = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationVersionPageToken, "application version page token is invalid", nil)
	ErrApplicationVersionManagementNotFound          = newError(ErrorCategoryNotFound, ErrorCodeApplicationVersionManagementNotFound, "application version management resource not found", nil)
	ErrApplicationVersionManagementStateInconsistent = newError(ErrorCategoryInternal, ErrorCodeApplicationVersionManagementStateInconsistent, "application version management state is inconsistent", nil)
	ErrInternal                                      = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", nil)
)
