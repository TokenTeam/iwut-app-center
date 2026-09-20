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
	ErrorCodeInvalidApplicationID                 ErrorCode = "InvalidApplicationId"
	ErrorCodeDeveloperIdentityRequired            ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired            ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidVersionLabel                  ErrorCode = "InvalidVersionLabel"
	ErrorCodeInvalidApplicationLaunchURL          ErrorCode = "InvalidApplicationLaunchUrl"
	ErrorCodeInvalidRPCApiRange                   ErrorCode = "InvalidRpcApiRange"
	ErrorCodeInvalidRequiredCapability            ErrorCode = "InvalidRequiredCapability"
	ErrorCodeInvalidApplicationScope              ErrorCode = "InvalidApplicationScope"
	ErrorCodeScopeCatalogUnavailable              ErrorCode = "ScopeCatalogUnavailable"
	ErrorCodeApplicationNotFound                  ErrorCode = "ApplicationNotFound"
	ErrorCodeApplicationAdminRequired             ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationVersionRevisionRequired   ErrorCode = "ApplicationVersionRevisionRequired"
	ErrorCodeApplicationVersionNotFound           ErrorCode = "ApplicationVersionNotFound"
	ErrorCodeApplicationVersionNotDraft           ErrorCode = "ApplicationVersionNotDraft"
	ErrorCodeApplicationVersionRevisionConflict   ErrorCode = "ApplicationVersionRevisionConflict"
	ErrorCodeApplicationVersionLabelAlreadyExists ErrorCode = "ApplicationVersionLabelAlreadyExists"
	ErrorCodeInternal                             ErrorCode = "Internal"
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
	ErrApplicationVersionRevisionConflict = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionRevisionConflict, "application version revision conflicts", nil,
	)
	ErrApplicationVersionLabelAlreadyExists = newError(
		ErrorCategoryConflict, ErrorCodeApplicationVersionLabelAlreadyExists, "application version label already exists", nil,
	)
	ErrInternal = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", nil)
)
