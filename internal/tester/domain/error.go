package domain

import "errors"

type ErrorCategory string

const (
	ErrorCodeInvalidTesterMembershipId           ErrorCode     = "InvalidTesterMembershipId"
	ErrorCodeApplicationTesterMembershipNotFound ErrorCode     = "ApplicationTesterMembershipNotFound"
	ErrorCodeApplicationTesterStateInconsistent  ErrorCode     = "ApplicationTesterStateInconsistent"
	ErrorCategoryValidation                      ErrorCategory = "Validation"
	ErrorCategoryAuthentication                  ErrorCategory = "Authentication"
	ErrorCategoryAuthorization                   ErrorCategory = "Authorization"
	ErrorCategoryNotFound                        ErrorCategory = "NotFound"
	ErrorCategoryConflict                        ErrorCategory = "Conflict"
	ErrorCategoryDependencyUnavailable           ErrorCategory = "DependencyUnavailable"
	ErrorCategoryInternal                        ErrorCategory = "Internal"
)

type ErrorCode string

const (
	ErrorCodeAuthenticatedUserRequired              ErrorCode = "AuthenticatedUserRequired"
	ErrorCodeInvalidTesterJoinSecret                ErrorCode = "InvalidTesterJoinSecret"
	ErrorCodeTesterJoinLinkInvalid                  ErrorCode = "TesterJoinLinkInvalid"
	ErrorCodeApplicationTesterLimitReached          ErrorCode = "ApplicationTesterLimitReached"
	ErrorCodeDeveloperIdentityRequired              ErrorCode = "DeveloperIdentityRequired"
	ErrorCodeDeveloperApprovalRequired              ErrorCode = "DeveloperApprovalRequired"
	ErrorCodeInvalidApplicationId                   ErrorCode = "InvalidApplicationId"
	ErrorCodeInvalidTesterJoinLinkId                ErrorCode = "InvalidTesterJoinLinkId"
	ErrorCodeApplicationNotFound                    ErrorCode = "ApplicationNotFound"
	ErrorCodeApplicationAdminRequired               ErrorCode = "ApplicationAdminRequired"
	ErrorCodeApplicationTesterJoinLinkAlreadyExists ErrorCode = "ApplicationTesterJoinLinkAlreadyExists"
	ErrorCodeApplicationTesterJoinLinkNotFound      ErrorCode = "ApplicationTesterJoinLinkNotFound"
	ErrorCodeApplicationTesterJoinLinkChanged       ErrorCode = "ApplicationTesterJoinLinkChanged"
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

func (e *Error) Error() string           { return e.message }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Category() ErrorCategory { return e.category }
func (e *Error) Code() ErrorCode         { return e.code }
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.code == other.code
}

var (
	ErrInvalidTesterMembershipId              = newError(ErrorCategoryValidation, ErrorCodeInvalidTesterMembershipId, "tester membership ID is invalid", nil)
	ErrApplicationTesterMembershipNotFound    = newError(ErrorCategoryNotFound, ErrorCodeApplicationTesterMembershipNotFound, "application tester membership not found", nil)
	ErrApplicationTesterStateInconsistent     = newError(ErrorCategoryInternal, ErrorCodeApplicationTesterStateInconsistent, "application tester state is inconsistent", nil)
	ErrAuthenticatedUserRequired              = newError(ErrorCategoryAuthentication, ErrorCodeAuthenticatedUserRequired, "authenticated user is required", nil)
	ErrInvalidTesterJoinSecret                = newError(ErrorCategoryValidation, ErrorCodeInvalidTesterJoinSecret, "tester join secret is invalid", nil)
	ErrTesterJoinLinkInvalid                  = newError(ErrorCategoryNotFound, ErrorCodeTesterJoinLinkInvalid, "tester join link is invalid", nil)
	ErrApplicationTesterLimitReached          = newError(ErrorCategoryConflict, ErrorCodeApplicationTesterLimitReached, "application tester limit reached", nil)
	ErrDeveloperIdentityRequired              = newError(ErrorCategoryAuthentication, ErrorCodeDeveloperIdentityRequired, "developer identity is required", nil)
	ErrDeveloperApprovalRequired              = newError(ErrorCategoryAuthorization, ErrorCodeDeveloperApprovalRequired, "approved developer status is required", nil)
	ErrInvalidApplicationId                   = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationId, "application ID is invalid", nil)
	ErrInvalidTesterJoinLinkId                = newError(ErrorCategoryValidation, ErrorCodeInvalidTesterJoinLinkId, "tester join link ID is invalid", nil)
	ErrApplicationNotFound                    = newError(ErrorCategoryNotFound, ErrorCodeApplicationNotFound, "application not found", nil)
	ErrApplicationAdminRequired               = newError(ErrorCategoryAuthorization, ErrorCodeApplicationAdminRequired, "application administrator is required", nil)
	ErrApplicationTesterJoinLinkAlreadyExists = newError(ErrorCategoryConflict, ErrorCodeApplicationTesterJoinLinkAlreadyExists, "active tester join link already exists", nil)
	ErrApplicationTesterJoinLinkNotFound      = newError(ErrorCategoryNotFound, ErrorCodeApplicationTesterJoinLinkNotFound, "active tester join link not found", nil)
	ErrApplicationTesterJoinLinkChanged       = newError(ErrorCategoryConflict, ErrorCodeApplicationTesterJoinLinkChanged, "active tester join link changed", nil)
	ErrInternal                               = newError(ErrorCategoryInternal, ErrorCodeInternal, "internal failure", nil)
)
