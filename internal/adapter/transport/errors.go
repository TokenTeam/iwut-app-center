package transport

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/application/domain"
)

// Stable Proto error reasons. They are the machine-readable contract clients
// branch on; the accompanying message is never parsed. Values mirror the
// ErrorReason enum in app_center/v1/application/error_reason.proto and are
// asserted mechanically by the API contract test.
const (
	ReasonInvalidApplicationName       = "ERROR_REASON_INVALID_APPLICATION_NAME"
	ReasonDeveloperIdentityRequired    = "ERROR_REASON_DEVELOPER_IDENTITY_REQUIRED"
	ReasonInvalidDeveloperIdentity     = "ERROR_REASON_INVALID_DEVELOPER_IDENTITY"
	ReasonDeveloperApprovalRequired    = "ERROR_REASON_DEVELOPER_APPROVAL_REQUIRED"
	ReasonApplicationNameAlreadyExists = "ERROR_REASON_APPLICATION_NAME_ALREADY_EXISTS"
	ReasonApplicationQuotaExceeded     = "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED"
	ReasonInternal                     = "ERROR_REASON_INTERNAL"
)

type errorSpec struct {
	code    codes.Code
	reason  string
	message string
}

// domainErrorSpecs is the single ADR-005 mapping table from protocol-independent
// domain codes to canonical gRPC status and stable reason. Kratos derives the
// HTTP status from the gRPC code, so one table serves both transports without
// branching on error text.
var domainErrorSpecs = map[domain.ErrorCode]errorSpec{
	domain.ErrorCodeInvalidApplicationName: {
		code:    codes.InvalidArgument,
		reason:  ReasonInvalidApplicationName,
		message: "application name is invalid",
	},
	domain.ErrorCodeDeveloperIdentityRequired: {
		code:    codes.Unauthenticated,
		reason:  ReasonDeveloperIdentityRequired,
		message: "developer identity is required",
	},
	domain.ErrorCodeDeveloperApprovalRequired: {
		code:    codes.PermissionDenied,
		reason:  ReasonDeveloperApprovalRequired,
		message: "approved developer status is required",
	},
	domain.ErrorCodeApplicationNameAlreadyExists: {
		code:    codes.AlreadyExists,
		reason:  ReasonApplicationNameAlreadyExists,
		message: "application name already exists",
	},
	domain.ErrorCodeApplicationQuotaExceeded: {
		code:    codes.ResourceExhausted,
		reason:  ReasonApplicationQuotaExceeded,
		message: "application creation quota exceeded",
	},
	// InvalidApplicationId is an internal corruption signal on this path; it is
	// never a caller validation error.
	domain.ErrorCodeInvalidApplicationID: {
		code:    codes.Internal,
		reason:  ReasonInternal,
		message: "internal failure",
	},
	domain.ErrorCodeInternal: {
		code:    codes.Internal,
		reason:  ReasonInternal,
		message: "internal failure",
	},
}

var internalSpec = errorSpec{
	code:    codes.Internal,
	reason:  ReasonInternal,
	message: "internal failure",
}

// toTransportError maps any error crossing the transport boundary. Unknown and
// infrastructure errors collapse to Internal without leaking cause or details.
func toTransportError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errIdentityRequired) {
		return transportStatus(codes.Unauthenticated, ReasonDeveloperIdentityRequired, "developer identity is required")
	}
	if errors.Is(err, errIdentityInvalid) {
		return transportStatus(codes.Unauthenticated, ReasonInvalidDeveloperIdentity, "developer identity is invalid")
	}

	var domainError *domain.Error
	if errors.As(err, &domainError) {
		spec, ok := domainErrorSpecs[domainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	return transportStatus(internalSpec.code, internalSpec.reason, internalSpec.message)
}

func transportStatus(code codes.Code, reason, message string) error {
	st := status.New(code, message)
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}
