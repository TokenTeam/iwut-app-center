package auth

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	systemprincipalv1 "iwut-app-center/api/gen/go/auth_center/v1/system_principal"
	reviewport "iwut-app-center/internal/review/port"
)

type fakeSystemPrincipalClient struct {
	response *systemprincipalv1.ResolveSystemPrincipalResponse
	err      error
	calls    int
}

func (client *fakeSystemPrincipalClient) ResolveSystemPrincipal(context.Context, *systemprincipalv1.ResolveSystemPrincipalRequest, ...grpc.CallOption) (*systemprincipalv1.ResolveSystemPrincipalResponse, error) {
	client.calls++
	return client.response, client.err
}

func TestGRPCSystemPrincipalResolver_CachesSuccessfulResponse(t *testing.T) {
	client := &fakeSystemPrincipalClient{response: &systemprincipalv1.ResolveSystemPrincipalResponse{
		AuthId: "auth-system", Purpose: systemprincipalv1.SystemPrincipalPurpose_SYSTEM_PRINCIPAL_PURPOSE_APP_CENTER_REVIEW_AUTO_REJECTION,
	}}
	resolver := newGRPCSystemPrincipalResolver(client)
	for range 2 {
		got, err := resolver.ResolveReviewAutoRejection(context.Background())
		if err != nil || got != "auth-system" {
			t.Fatalf("ResolveReviewAutoRejection() = (%q, %v)", got, err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
}

func TestGRPCSystemPrincipalResolver_DoesNotCacheFailures(t *testing.T) {
	client := &fakeSystemPrincipalClient{err: errors.New("unavailable")}
	resolver := newGRPCSystemPrincipalResolver(client)
	for range 2 {
		_, err := resolver.ResolveReviewAutoRejection(context.Background())
		if !errors.Is(err, reviewport.ErrSystemPrincipalUnavailable) {
			t.Fatalf("error = %v, want system principal unavailable", err)
		}
	}
	if client.calls != 2 {
		t.Fatalf("client calls = %d, want 2", client.calls)
	}
}
