package transport

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/shared"
)

type fakeTransporter struct {
	kind      transport.Kind
	header    transport.Header
	operation string
}

func (transporter fakeTransporter) Kind() transport.Kind            { return transporter.kind }
func (transporter fakeTransporter) Endpoint() string                { return "test://endpoint" }
func (transporter fakeTransporter) Operation() string               { return transporter.operation }
func (transporter fakeTransporter) RequestHeader() transport.Header { return transporter.header }
func (transporter fakeTransporter) ReplyHeader() transport.Header   { return transporter.header }

type httpHeader struct {
	values http.Header
}

func (header httpHeader) Get(key string) string { return header.values.Get(key) }
func (header httpHeader) Set(key, value string) { header.values.Set(key, value) }
func (header httpHeader) Add(key, value string) { header.values.Add(key, value) }
func (header httpHeader) Keys() []string {
	keys := make([]string, 0, len(header.values))
	for key := range header.values {
		keys = append(keys, key)
	}
	return keys
}
func (header httpHeader) Values(key string) []string { return header.values.Values(key) }

type grpcHeader struct {
	values metadata.MD
}

func (header grpcHeader) Get(key string) string {
	values := header.values.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
func (header grpcHeader) Set(key, value string) { header.values.Set(key, value) }
func (header grpcHeader) Add(key, value string) { header.values.Append(key, value) }
func (header grpcHeader) Keys() []string {
	keys := make([]string, 0, len(header.values))
	for key := range header.values {
		keys = append(keys, key)
	}
	return keys
}
func (header grpcHeader) Values(key string) []string { return header.values.Get(key) }

func contextWithHTTPHeader(header http.Header) context.Context {
	return transport.NewServerContext(context.Background(), fakeTransporter{
		kind:   transport.KindHTTP,
		header: httpHeader{values: header},
	})
}

func contextWithGRPCMetadata(md metadata.MD) context.Context {
	return transport.NewServerContext(context.Background(), fakeTransporter{
		kind:   transport.KindGRPC,
		header: grpcHeader{values: md},
	})
}

func httpContextWithIdentity(token string) context.Context {
	header := http.Header{}
	header.Set(IdentityHeader, token)
	return contextWithHTTPHeader(header)
}

func runIdentityMiddleware(t *testing.T, verifier *IdentityVerifier, ctx context.Context) (shared.DeveloperIdentity, error) {
	t.Helper()
	var captured shared.DeveloperIdentity
	handler := identityMiddleware(verifier)(func(ctx context.Context, request any) (any, error) {
		identity, ok := developerIdentityFromContext(ctx)
		if !ok {
			t.Fatal("identity missing from handler context")
		}
		captured = identity
		return nil, nil
	})
	_, err := handler(ctx, nil)
	return captured, err
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	statusError, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status error", err)
	}
	for _, detail := range statusError.Details() {
		if info, ok := detail.(interface{ GetReason() string }); ok {
			return info.GetReason()
		}
	}
	t.Fatalf("error %v has no ErrorInfo reason detail", err)
	return ""
}

func assertTransportError(t *testing.T, err error, wantCode codes.Code, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	statusError, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status error", err)
	}
	if statusError.Code() != wantCode {
		t.Fatalf("code = %v, want %v (error %v)", statusError.Code(), wantCode, err)
	}
	if reason := reasonOf(t, err); reason != wantReason {
		t.Fatalf("reason = %q, want %q", reason, wantReason)
	}
}

func TestIdentityMiddleware_HTTPHeaderAndGRPCMetadataAgree(t *testing.T) {
	t.Parallel()

	verifier := newTestVerifier(t)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})

	testCases := []struct {
		name string
		ctx  context.Context
	}{
		{name: "http header", ctx: httpContextWithIdentity(token)},
		{name: "grpc metadata", ctx: contextWithGRPCMetadata(metadata.Pairs(IdentityHeader, token))},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			identity, err := runIdentityMiddleware(t, verifier, testCase.ctx)
			if err != nil {
				t.Fatalf("middleware error = %v", err)
			}
			if identity.AuthID != shared.AuthID(tokenSubject) || identity.DeveloperStatus != shared.DeveloperStatusApproved {
				t.Fatalf("identity = %#v", identity)
			}
		})
	}
}

func TestIdentityMiddleware_RejectsMissingAndLegacyIdentity(t *testing.T) {
	t.Parallel()

	verifier := newTestVerifier(t)

	t.Run("missing identity", func(t *testing.T) {
		t.Parallel()
		_, err := runIdentityMiddleware(t, verifier, contextWithHTTPHeader(http.Header{}))
		assertTransportError(t, err, codes.Unauthenticated, ReasonDeveloperIdentityRequired)
	})

	t.Run("legacy unsigned header is not accepted", func(t *testing.T) {
		t.Parallel()
		header := http.Header{}
		header.Set("X-Auth-Base-Claim", `{"uid":"forged","type":"access"}`)
		_, err := runIdentityMiddleware(t, verifier, contextWithHTTPHeader(header))
		assertTransportError(t, err, codes.Unauthenticated, ReasonInvalidDeveloperIdentity)
	})

	t.Run("multiple identity values", func(t *testing.T) {
		t.Parallel()
		md := metadata.Pairs(IdentityHeader, "one", IdentityHeader, "two")
		_, err := runIdentityMiddleware(t, verifier, contextWithGRPCMetadata(md))
		assertTransportError(t, err, codes.Unauthenticated, ReasonInvalidDeveloperIdentity)
	})
}

func TestIdentityMiddleware_UsesReviewerReasonsForDecisionOperation(t *testing.T) {
	t.Parallel()

	verifier := newTestVerifier(t)
	ctx := transport.NewServerContext(context.Background(), fakeTransporter{
		kind:      transport.KindGRPC,
		header:    grpcHeader{values: metadata.MD{}},
		operation: DecideApplicationVersionReviewGRPCMethod,
	})
	_, err := runIdentityMiddleware(t, verifier, ctx)
	assertTransportError(t, err, codes.Unauthenticated, ReasonReviewerIdentityRequired)

	invalidCtx := transport.NewServerContext(context.Background(), fakeTransporter{
		kind:      transport.KindGRPC,
		header:    grpcHeader{values: metadata.Pairs(IdentityHeader, "not-a-jws")},
		operation: DecideApplicationVersionReviewGRPCMethod,
	})
	_, err = runIdentityMiddleware(t, verifier, invalidCtx)
	assertTransportError(t, err, codes.Unauthenticated, ReasonInvalidReviewerIdentity)
}

func TestIdentityMiddleware_AllowsInfrastructureRPCsWithoutIdentity(t *testing.T) {
	t.Parallel()

	verifier := newTestVerifier(t)
	for _, operation := range []string{
		"/grpc.health.v1.Health/Check",
		"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
	} {
		ctx := transport.NewServerContext(context.Background(), fakeTransporter{
			kind:      transport.KindGRPC,
			header:    grpcHeader{values: metadata.MD{}},
			operation: operation,
		})
		called := false
		handler := identityMiddleware(verifier)(func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
		if _, err := handler(ctx, nil); err != nil {
			t.Fatalf("%s: middleware error = %v", operation, err)
		}
		if !called {
			t.Fatalf("%s: handler was not called", operation)
		}
	}

	businessCtx := transport.NewServerContext(context.Background(), fakeTransporter{
		kind:      transport.KindGRPC,
		header:    grpcHeader{values: metadata.MD{}},
		operation: CreateApplicationGRPCMethod,
	})
	_, err := runIdentityMiddleware(t, verifier, businessCtx)
	assertTransportError(t, err, codes.Unauthenticated, ReasonDeveloperIdentityRequired)
}
