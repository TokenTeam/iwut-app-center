package auth

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"

	systemprincipalv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/system_principal"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

type GRPCSystemPrincipalResolver struct {
	client systemPrincipalClient
	mu     sync.RWMutex
	cached shared.AuthID
}

type systemPrincipalClient interface {
	ResolveSystemPrincipal(context.Context, *systemprincipalv1.ResolveSystemPrincipalRequest, ...grpc.CallOption) (*systemprincipalv1.ResolveSystemPrincipalResponse, error)
}

func NewGRPCSystemPrincipalResolver(connection *grpc.ClientConn) (*GRPCSystemPrincipalResolver, error) {
	if connection == nil {
		return nil, fmt.Errorf("Auth System Principal gRPC connection is required")
	}
	return &GRPCSystemPrincipalResolver{client: systemprincipalv1.NewSystemPrincipalDirectoryClient(connection)}, nil
}

func newGRPCSystemPrincipalResolver(client systemPrincipalClient) *GRPCSystemPrincipalResolver {
	return &GRPCSystemPrincipalResolver{client: client}
}

func (resolver *GRPCSystemPrincipalResolver) ResolveReviewAutoRejection(ctx context.Context) (shared.AuthID, error) {
	if resolver == nil || resolver.client == nil {
		return "", reviewport.ErrSystemPrincipalUnavailable
	}
	resolver.mu.RLock()
	cached := resolver.cached
	resolver.mu.RUnlock()
	if cached.IsValid() {
		return cached, nil
	}
	response, err := resolver.client.ResolveSystemPrincipal(ctx, &systemprincipalv1.ResolveSystemPrincipalRequest{
		Purpose: systemprincipalv1.SystemPrincipalPurpose_SYSTEM_PRINCIPAL_PURPOSE_APP_CENTER_REVIEW_AUTO_REJECTION,
	})
	if err != nil {
		return "", fmt.Errorf("%w: resolve Auth System Principal: %v", reviewport.ErrSystemPrincipalUnavailable, err)
	}
	if response == nil || response.GetPurpose() != systemprincipalv1.SystemPrincipalPurpose_SYSTEM_PRINCIPAL_PURPOSE_APP_CENTER_REVIEW_AUTO_REJECTION || response.GetAuthId() == "" {
		return "", fmt.Errorf("%w: invalid Auth System Principal response", reviewport.ErrSystemPrincipalUnavailable)
	}
	resolved := shared.AuthID(response.GetAuthId())
	resolver.mu.Lock()
	if !resolver.cached.IsValid() {
		resolver.cached = resolved
	}
	resolved = resolver.cached
	resolver.mu.Unlock()
	return resolved, nil
}

var _ reviewport.SystemPrincipalResolver = (*GRPCSystemPrincipalResolver)(nil)
