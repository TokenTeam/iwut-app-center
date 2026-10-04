package domain

import "errors"

type ErrorCategory string
type ErrorCode string

const (
	ErrorCategoryValidation                         ErrorCategory = "Validation"
	ErrorCategoryAuthentication                     ErrorCategory = "Authentication"
	ErrorCategoryAuthorization                      ErrorCategory = "Authorization"
	ErrorCategoryNotFound                           ErrorCategory = "NotFound"
	ErrorCategoryDependencyUnavailable              ErrorCategory = "DependencyUnavailable"
	ErrorCategoryInternal                           ErrorCategory = "Internal"
	ErrorCodeAuthenticatedUserRequired              ErrorCode     = "AuthenticatedUserRequired"
	ErrorCodeInvalidApplicationID                   ErrorCode     = "InvalidApplicationId"
	ErrorCodeInvalidHostRPCAPIMajor                 ErrorCode     = "InvalidHostRpcApiMajor"
	ErrorCodeInvalidHostCapabilities                ErrorCode     = "InvalidHostCapabilities"
	ErrorCodeApplicationNotFound                    ErrorCode     = "ApplicationNotFound"
	ErrorCodeApplicationTesterRequired              ErrorCode     = "ApplicationTesterRequired"
	ErrorCodeApplicationTestTargetUnavailable       ErrorCode     = "ApplicationTestTargetUnavailable"
	ErrorCodeHostCapabilitiesInsufficient           ErrorCode     = "HostCapabilitiesInsufficient"
	ErrorCodeApplicationTestPublicationInconsistent ErrorCode     = "ApplicationTestPublicationInconsistent"
	ErrorCodeApplicationLaunchTargetUnavailable     ErrorCode     = "ApplicationLaunchTargetUnavailable"
	ErrorCodeApplicationRuntimeStateInconsistent    ErrorCode     = "ApplicationRuntimeStateInconsistent"
	ErrorCodeInternal                               ErrorCode     = "Internal"
)

type Error struct {
	category            ErrorCategory
	code                ErrorCode
	message             string
	cause               error
	missingCapabilities []CapabilityName
}

func newError(category ErrorCategory, code ErrorCode, message string) *Error {
	return &Error{category: category, code: code, message: message}
}
func (e *Error) Error() string           { return e.message }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }
func (e *Error) MissingCapabilities() []CapabilityName {
	return append([]CapabilityName{}, e.missingCapabilities...)
}
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}
func NewInternalError(cause error) *Error {
	e := newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure")
	e.cause = cause
	return e
}
func NewHostCapabilitiesInsufficientError(missing []CapabilityName) *Error {
	values := make([]string, len(missing))
	for i, v := range missing {
		values[i] = string(v)
	}
	normalized, err := NormalizeHostCapabilities(values)
	if err != nil || len(normalized) == 0 {
		return NewInternalError(nil)
	}
	e := newError(ErrorCategoryValidation, ErrorCodeHostCapabilitiesInsufficient, "host capabilities are insufficient")
	e.missingCapabilities = normalized
	return e
}

var (
	ErrAuthenticatedUserRequired              = newError(ErrorCategoryAuthentication, ErrorCodeAuthenticatedUserRequired, "authenticated user is required")
	ErrInvalidApplicationID                   = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationID, "application ID is invalid")
	ErrInvalidHostRPCAPIMajor                 = newError(ErrorCategoryValidation, ErrorCodeInvalidHostRPCAPIMajor, "host RPC API major is invalid")
	ErrInvalidHostCapabilities                = newError(ErrorCategoryValidation, ErrorCodeInvalidHostCapabilities, "host capabilities are invalid")
	ErrApplicationNotFound                    = newError(ErrorCategoryNotFound, ErrorCodeApplicationNotFound, "application not found")
	ErrApplicationTesterRequired              = newError(ErrorCategoryAuthorization, ErrorCodeApplicationTesterRequired, "active application tester membership is required")
	ErrApplicationTestTargetUnavailable       = newError(ErrorCategoryNotFound, ErrorCodeApplicationTestTargetUnavailable, "application test target is unavailable")
	ErrHostCapabilitiesInsufficient           = newError(ErrorCategoryValidation, ErrorCodeHostCapabilitiesInsufficient, "host capabilities are insufficient")
	ErrApplicationTestPublicationInconsistent = newError(ErrorCategoryDependencyUnavailable, ErrorCodeApplicationTestPublicationInconsistent, "application test publication is temporarily unavailable")
	ErrApplicationLaunchTargetUnavailable     = newError(ErrorCategoryNotFound, ErrorCodeApplicationLaunchTargetUnavailable, "application launch target is unavailable")
	ErrApplicationRuntimeStateInconsistent    = newError(ErrorCategoryInternal, ErrorCodeApplicationRuntimeStateInconsistent, "application runtime state is inconsistent")
	ErrInternal                               = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure")
)
