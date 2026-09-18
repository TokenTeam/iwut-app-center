package domain

import "errors"

// ErrorCategory classifies an application error independently of any transport.
type ErrorCategory string

const (
	ErrorCategoryValidation     ErrorCategory = "Validation"
	ErrorCategoryAuthentication ErrorCategory = "Authentication"
	ErrorCategoryAuthorization  ErrorCategory = "Authorization"
	ErrorCategoryConflict       ErrorCategory = "Conflict"
	ErrorCategoryInternal       ErrorCategory = "Internal"
)

// ErrorCode is a stable identifier that callers may use for branching.
type ErrorCode string

const (
	ErrorCodeInvalidApplicationID         ErrorCode = "InvalidApplicationId"
	ErrorCodeInvalidApplicationName       ErrorCode = "InvalidApplicationName"
	ErrorCodeDeveloperIdentityRequired    ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired    ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeApplicationNameAlreadyExists ErrorCode = "ApplicationNameAlreadyExists"
	ErrorCodeApplicationQuotaExceeded     ErrorCode = "ApplicationQuotaExceeded"
	ErrorCodeInternal                     ErrorCode = "Internal"
)

// Error is a protocol-independent application error.
type Error struct {
	category ErrorCategory
	code     ErrorCode
	message  string
	cause    error
}

func newError(category ErrorCategory, code ErrorCode, message string, cause error) *Error {
	return &Error{category: category, code: code, message: message, cause: cause}
}

// NewInternalError retains a dependency failure without exposing it as a
// business-facing message.
func NewInternalError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", cause)
}

func (e *Error) Error() string {
	return e.message
}

func (e *Error) Unwrap() error {
	return e.cause
}

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}

func (e *Error) Category() ErrorCategory {
	return e.category
}

func (e *Error) Code() ErrorCode {
	return e.code
}

var (
	ErrInvalidApplicationID = newError(
		ErrorCategoryValidation,
		ErrorCodeInvalidApplicationID,
		"application ID is invalid",
		nil,
	)
	ErrInvalidApplicationName = newError(
		ErrorCategoryValidation,
		ErrorCodeInvalidApplicationName,
		"application name is invalid",
		nil,
	)
	ErrDeveloperIdentityRequired = newError(
		ErrorCategoryAuthentication,
		ErrorCodeDeveloperIdentityRequired,
		"developer identity is required",
		nil,
	)
	ErrDeveloperApprovalRequired = newError(
		ErrorCategoryAuthorization,
		ErrorCodeDeveloperApprovalRequired,
		"approved developer status is required",
		nil,
	)
	ErrApplicationNameAlreadyExists = newError(
		ErrorCategoryConflict,
		ErrorCodeApplicationNameAlreadyExists,
		"application name already exists",
		nil,
	)
	ErrApplicationQuotaExceeded = newError(
		ErrorCategoryConflict,
		ErrorCodeApplicationQuotaExceeded,
		"application creation quota exceeded",
		nil,
	)
	ErrInternal = newError(
		ErrorCategoryInternal,
		ErrorCodeInternal,
		"internal failure",
		nil,
	)
)
