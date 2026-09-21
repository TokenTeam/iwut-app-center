package transport

import (
	"context"
	"errors"
	"strings"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"

	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
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
			}
			if hasLegacyIdentityHeader(ctx) {
				return nil, toIdentityTransportError(errIdentityInvalid, operation)
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
	if operation == applicationreviewv1.OperationApplicationReviewDecideApplicationVersionReview {
		if errors.Is(err, errIdentityRequired) {
			return transportStatus(codes.Unauthenticated, ReasonReviewerIdentityRequired, "reviewer identity is required")
		}
		return transportStatus(codes.Unauthenticated, ReasonInvalidReviewerIdentity, "reviewer identity is invalid")
	}
	return toTransportError(err)
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
