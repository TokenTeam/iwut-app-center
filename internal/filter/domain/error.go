package domain

import "errors"

type ErrorCategory string
type ErrorCode string

const (
	ErrorCategoryValidation     ErrorCategory = "Validation"
	ErrorCategoryAuthentication ErrorCategory = "Authentication"
	ErrorCategoryAuthorization  ErrorCategory = "Authorization"
	ErrorCategoryNotFound       ErrorCategory = "NotFound"
	ErrorCategoryConflict       ErrorCategory = "Conflict"
	ErrorCategoryInternal       ErrorCategory = "Internal"

	ErrorCodeInvalidApplicationID               ErrorCode = "InvalidApplicationId"
	ErrorCodeDeveloperIdentityRequired          ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired          ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidApplicationFilter           ErrorCode = "InvalidApplicationFilter"
	ErrorCodeApplicationNotFound                ErrorCode = "ApplicationNotFound"
	ErrorCodeApplicationAdminRequired           ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationFilterRevisionConflict  ErrorCode = "ApplicationFilterRevisionConflict"
	ErrorCodeApplicationFilterStateInconsistent ErrorCode = "ApplicationFilterStateInconsistent"
	ErrorCodeInternal                           ErrorCode = "Internal"
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
func NewStateInconsistentError(cause error) *Error {
	return newError(ErrorCategoryInternal, ErrorCodeApplicationFilterStateInconsistent, "application filter state is inconsistent", cause)
}

var (
	ErrInvalidApplicationID               = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationID, "application ID is invalid", nil)
	ErrDeveloperIdentityRequired          = newError(ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil)
	ErrDeveloperApprovalRequired          = newError(ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil)
	ErrInvalidApplicationFilter           = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationFilter, "application filter is invalid", nil)
	ErrApplicationNotFound                = newError(ErrorCategoryNotFound, ErrorCodeApplicationNotFound, "application not found", nil)
	ErrApplicationAdminRequired           = newError(ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil)
	ErrApplicationFilterRevisionConflict  = newError(ErrorCategoryConflict, ErrorCodeApplicationFilterRevisionConflict, "application filter revision conflict", nil)
	ErrApplicationFilterStateInconsistent = NewStateInconsistentError(nil)
	ErrInternal                           = NewInternalError(nil)
)
