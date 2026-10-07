package domain

import "errors"

type ErrorCode string
type ErrorCategory string

const (
	ErrorCategoryValidation     ErrorCategory = "Validation"
	ErrorCategoryAuthentication ErrorCategory = "Authentication"
	ErrorCategoryNotFound       ErrorCategory = "NotFound"
	ErrorCategoryInternal       ErrorCategory = "Internal"

	ErrorCodeAuthenticatedUserRequired              ErrorCode = "AuthenticatedUserRequired"
	ErrorCodeInvalidApplicationManagementRequest    ErrorCode = "InvalidApplicationManagementRequest"
	ErrorCodeInvalidApplicationManagementPageToken  ErrorCode = "InvalidApplicationManagementPageToken"
	ErrorCodeApplicationManagementNotFound          ErrorCode = "ApplicationManagementNotFound"
	ErrorCodeApplicationManagementStateInconsistent ErrorCode = "ApplicationManagementStateInconsistent"
	ErrorCodeInternal                               ErrorCode = "Internal"
)

type Error struct {
	category ErrorCategory
	code     ErrorCode
	message  string
	cause    error
}

func (e *Error) Error() string { return e.message }
func (e *Error) Unwrap() error { return e.cause }
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }
func newError(category ErrorCategory, code ErrorCode, message string) *Error {
	return &Error{category: category, code: code, message: message}
}
func NewInternalError(cause error) *Error {
	return &Error{category: ErrorCategoryInternal, code: ErrorCodeInternal, message: "internal failure", cause: cause}
}

var (
	ErrAuthenticatedUserRequired = newError(ErrorCategoryAuthentication, ErrorCodeAuthenticatedUserRequired, "authenticated user is required")
	ErrInvalidRequest            = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationManagementRequest, "application management request is invalid")
	ErrInvalidPageToken          = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationManagementPageToken, "application management page token is invalid")
	ErrNotFound                  = newError(ErrorCategoryNotFound, ErrorCodeApplicationManagementNotFound, "application management resource was not found")
	ErrStateInconsistent         = newError(ErrorCategoryInternal, ErrorCodeApplicationManagementStateInconsistent, "application management state is inconsistent")
)
