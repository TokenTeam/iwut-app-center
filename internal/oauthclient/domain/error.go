package domain

import "errors"

type ErrorCode string

const (
	ErrorCodeDeveloperIdentityRequired    ErrorCode = "DEVELOPER_IDENTITY_REQUIRED"
	ErrorCodeDeveloperApprovalRequired    ErrorCode = "DEVELOPER_APPROVAL_REQUIRED"
	ErrorCodeInvalidApplicationID         ErrorCode = "INVALID_APPLICATION_ID"
	ErrorCodeInvalidOAuthChannel          ErrorCode = "INVALID_OAUTH_CHANNEL"
	ErrorCodeOAuthChannelNotEnabled       ErrorCode = "OAUTH_CHANNEL_NOT_ENABLED"
	ErrorCodeInvalidOAuthClientType       ErrorCode = "INVALID_OAUTH_CLIENT_TYPE"
	ErrorCodeInvalidOAuthClientID         ErrorCode = "INVALID_OAUTH_CLIENT_ID"
	ErrorCodeInvalidOAuthClientStatus     ErrorCode = "INVALID_OAUTH_CLIENT_STATUS"
	ErrorCodeInvalidRegistrationRevision  ErrorCode = "INVALID_OAUTH_REGISTRATION_REVISION"
	ErrorCodeInvalidCredentialRevision    ErrorCode = "INVALID_OAUTH_CREDENTIAL_REVISION"
	ErrorCodeApplicationNotFound          ErrorCode = "APPLICATION_NOT_FOUND"
	ErrorCodeApplicationAdminRequired     ErrorCode = "APPLICATION_ADMIN_REQUIRED"
	ErrorCodeOAuthRegistrationNotFound    ErrorCode = "OAUTH_REGISTRATION_NOT_FOUND"
	ErrorCodeOAuthClientAlreadyExists     ErrorCode = "OAUTH_CLIENT_ALREADY_EXISTS"
	ErrorCodeOAuthClientNotFound          ErrorCode = "OAUTH_CLIENT_NOT_FOUND"
	ErrorCodeOAuthRegistrationChanged     ErrorCode = "OAUTH_REGISTRATION_CHANGED"
	ErrorCodeOAuthCredentialNotFound      ErrorCode = "OAUTH_CLIENT_CREDENTIAL_NOT_FOUND"
	ErrorCodeOAuthCredentialChanged       ErrorCode = "OAUTH_CLIENT_CREDENTIAL_CHANGED"
	ErrorCodeOAuthClientStateInconsistent ErrorCode = "OAUTH_CLIENT_STATE_INCONSISTENT"
	ErrorCodeInternal                     ErrorCode = "INTERNAL"
)

type Error struct {
	code  ErrorCode
	cause error
}

func (e *Error) Error() string           { return string(e.code) }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Code() ErrorCode         { return e.code }
func NewError(code ErrorCode) error      { return &Error{code: code} }
func NewInternalError(cause error) error { return &Error{code: ErrorCodeInternal, cause: cause} }

func IsCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.code == code
}

var (
	ErrDeveloperIdentityRequired    = NewError(ErrorCodeDeveloperIdentityRequired)
	ErrDeveloperApprovalRequired    = NewError(ErrorCodeDeveloperApprovalRequired)
	ErrInvalidApplicationID         = NewError(ErrorCodeInvalidApplicationID)
	ErrInvalidOAuthChannel          = NewError(ErrorCodeInvalidOAuthChannel)
	ErrOAuthChannelNotEnabled       = NewError(ErrorCodeOAuthChannelNotEnabled)
	ErrInvalidOAuthClientType       = NewError(ErrorCodeInvalidOAuthClientType)
	ErrInvalidOAuthClientID         = NewError(ErrorCodeInvalidOAuthClientID)
	ErrInvalidOAuthClientStatus     = NewError(ErrorCodeInvalidOAuthClientStatus)
	ErrInvalidRegistrationRevision  = NewError(ErrorCodeInvalidRegistrationRevision)
	ErrInvalidCredentialRevision    = NewError(ErrorCodeInvalidCredentialRevision)
	ErrApplicationNotFound          = NewError(ErrorCodeApplicationNotFound)
	ErrApplicationAdminRequired     = NewError(ErrorCodeApplicationAdminRequired)
	ErrOAuthRegistrationNotFound    = NewError(ErrorCodeOAuthRegistrationNotFound)
	ErrOAuthClientAlreadyExists     = NewError(ErrorCodeOAuthClientAlreadyExists)
	ErrOAuthClientNotFound          = NewError(ErrorCodeOAuthClientNotFound)
	ErrOAuthRegistrationChanged     = NewError(ErrorCodeOAuthRegistrationChanged)
	ErrOAuthCredentialNotFound      = NewError(ErrorCodeOAuthCredentialNotFound)
	ErrOAuthCredentialChanged       = NewError(ErrorCodeOAuthCredentialChanged)
	ErrOAuthClientStateInconsistent = NewError(ErrorCodeOAuthClientStateInconsistent)
)
