package transport

import (
	"context"
	"errors"
	"strings"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"

	applicationoperationsv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_operations"
	profilereviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	testermembershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
)

// legacyIdentityHeaders are the unsigned JSON carriers of the retired system.
// Their presence is treated as an invalid identity, never as a fallback.
var legacyIdentityHeaders = []string{
	"x-auth-jwt-type",
	"x-auth-base-claim",
	"x-auth-oauth-claim",
	"x-auth-service-claim",
}

// unauthenticatedOperationPrefixes are infrastructure-only RPCs that must stay
// reachable without a developer identity so orchestration probes and tooling
// keep working. They never reach a business use case.
var unauthenticatedOperationPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.",
	"/livez",
	"/readyz",
}

func isUnauthenticatedOperation(operation string) bool {
	for _, prefix := range unauthenticatedOperationPrefixes {
		if strings.HasPrefix(operation, prefix) {
			return true
		}
	}
	return false
}

// identityMiddleware is a Kratos middleware shared by the HTTP and gRPC
// servers. It extracts the JWS from the transport header/metadata, verifies it
// locally and injects the resulting TrustedIdentity into the request context.
func identityMiddleware(verifier *IdentityVerifier) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			if transporter, ok := transport.FromServerContext(ctx); ok && isUnauthenticatedOperation(transporter.Operation()) {
				return handler(ctx, request)
			}
			operation := ""
			if transporter, ok := transport.FromServerContext(ctx); ok {
				operation = transporter.Operation()
				if operation == ResolveTestLaunchTargetGRPCMethod || operation == ResolveLaunchTargetGRPCMethod || operation == ListPublicApplicationsGRPCMethod || operation == GetPublicApplicationGRPCMethod {
					transporter.ReplyHeader().Set("Cache-Control", "private, no-store")
				}
				if operation == testermembershipv1.OperationTesterMembershipJoinApplicationAsTester || operation == testermembershipv1.OperationTesterMembershipRemoveApplicationTester {
					transporter.ReplyHeader().Set("Cache-Control", "no-store")
				}
			}
			if hasLegacyIdentityHeader(ctx) {
				return nil, toIdentityTransportError(errIdentityInvalid, operation)
			}
			if operation == ResolveLaunchTargetGRPCMethod || operation == ListPublicApplicationsGRPCMethod || operation == GetPublicApplicationGRPCMethod {
				token, present, err := optionalIdentityTokenFromContext(ctx)
				if err != nil {
					return nil, toIdentityTransportError(err, operation)
				}
				if !present {
					return handler(ctx, request)
				}
				identity, err := verifier.Verify(token)
				if err != nil {
					return nil, toIdentityTransportError(err, operation)
				}
				return handler(withTrustedIdentity(ctx, identity), request)
			}
			token, err := identityTokenFromContext(ctx)
			if err != nil {
				return nil, toIdentityTransportError(err, operation)
			}
			identity, err := verifier.Verify(token)
			if err != nil {
				return nil, toIdentityTransportError(err, operation)
			}
			return handler(withTrustedIdentity(ctx, identity), request)
		}
	}
}

func toIdentityTransportError(err error, operation string) error {
	if operation == applicationoperationsv1.OperationApplicationOperationsServiceGetApplicationPlatformAvailability || operation == applicationoperationsv1.OperationApplicationOperationsServiceSuspendApplication || operation == applicationoperationsv1.OperationApplicationOperationsServiceRestoreApplication {
		if errors.Is(err, errIdentityRequired) {
			return transportStatus(codes.Unauthenticated, "ERROR_REASON_USER_IDENTITY_REQUIRED", "user identity is required")
		}
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_INVALID_USER_IDENTITY", "user identity is invalid")
	}
	if operation == ResolveLaunchTargetGRPCMethod || operation == ListPublicApplicationsGRPCMethod || operation == GetPublicApplicationGRPCMethod {
		return transportStatus(codes.Unauthenticated, ReasonInvalidAuthenticatedUser, "authenticated user identity is invalid")
	}
	if operation == testermembershipv1.OperationTesterMembershipJoinApplicationAsTester || operation == ResolveTestLaunchTargetGRPCMethod {
		if errors.Is(err, errIdentityRequired) {
			return transportStatus(codes.Unauthenticated, ReasonAuthenticatedUserRequired, "authenticated user is required")
		}
		return transportStatus(codes.Unauthenticated, ReasonInvalidAuthenticatedUser, "authenticated user identity is invalid")
	}
	if operation == applicationreviewv1.OperationApplicationReviewDecideApplicationVersionReview || operation == profilereviewv1.OperationApplicationProfileReviewDecideApplicationProfileRevisionReview {
		if errors.Is(err, errIdentityRequired) {
			return transportStatus(codes.Unauthenticated, ReasonReviewerIdentityRequired, "reviewer identity is required")
		}
		return transportStatus(codes.Unauthenticated, ReasonInvalidReviewerIdentity, "reviewer identity is invalid")
	}
	return toTransportError(err)
}

func optionalIdentityTokenFromContext(ctx context.Context) (string, bool, error) {
	transporter, ok := transport.FromServerContext(ctx)
	if !ok {
		return "", false, errIdentityInvalid
	}
	values := transporter.RequestHeader().Values(IdentityHeader)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, errIdentityInvalid
	}
	token := strings.TrimSpace(values[0])
	if token == "" {
		return "", false, errIdentityInvalid
	}
	return token, true, nil
}

func identityTokenFromContext(ctx context.Context) (string, error) {
	transporter, ok := transport.FromServerContext(ctx)
	if !ok {
		return "", errIdentityRequired
	}
	values := transporter.RequestHeader().Values(IdentityHeader)
	if len(values) == 0 {
		return "", errIdentityRequired
	}
	if len(values) > 1 {
		return "", errIdentityInvalid
	}
	token := strings.TrimSpace(values[0])
	if token == "" {
		return "", errIdentityRequired
	}
	return token, nil
}

func hasLegacyIdentityHeader(ctx context.Context) bool {
	transporter, ok := transport.FromServerContext(ctx)
	if !ok {
		return false
	}
	header := transporter.RequestHeader()
	for _, key := range legacyIdentityHeaders {
		for _, value := range header.Values(key) {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}
