package auth

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	scopecatalogv1 "iwut-app-center/api/gen/go/auth_center/v1/scope_catalog"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

// GRPCScopeCatalogSnapshotSource adapts the shared Auth Scope Catalog v1 wire
// contract to App Center's deliberately narrow cache input.
type GRPCScopeCatalogSnapshotSource struct {
	client scopecatalogv1.ScopeCatalogClient
}

func NewGRPCScopeCatalogSnapshotSource(connection *grpc.ClientConn) (*GRPCScopeCatalogSnapshotSource, error) {
	if connection == nil {
		return nil, fmt.Errorf("Auth Scope Catalog gRPC connection is required")
	}
	return &GRPCScopeCatalogSnapshotSource{client: scopecatalogv1.NewScopeCatalogClient(connection)}, nil
}

func (source *GRPCScopeCatalogSnapshotSource) FetchScopeCatalogSnapshot(ctx context.Context) (ScopeCatalogSnapshot, error) {
	if source == nil || source.client == nil {
		return ScopeCatalogSnapshot{}, fmt.Errorf("fetch Auth Scope Catalog snapshot: client is unavailable")
	}
	response, err := source.client.GetScopeCatalogSnapshot(ctx, &scopecatalogv1.GetScopeCatalogSnapshotRequest{})
	if err != nil {
		return ScopeCatalogSnapshot{}, fmt.Errorf("fetch Auth Scope Catalog snapshot: %w", err)
	}
	return scopeCatalogSnapshotFromProto(response)
}

func scopeCatalogSnapshotFromProto(response *scopecatalogv1.GetScopeCatalogSnapshotResponse) (ScopeCatalogSnapshot, error) {
	if response == nil || response.GetRevision() <= 0 || response.GetGeneratedAt() == nil {
		return ScopeCatalogSnapshot{}, fmt.Errorf("invalid Auth Scope Catalog snapshot metadata")
	}
	if err := response.GetGeneratedAt().CheckValid(); err != nil {
		return ScopeCatalogSnapshot{}, fmt.Errorf("invalid Auth Scope Catalog generated_at: %w", err)
	}
	generatedAt := response.GetGeneratedAt().AsTime().UTC()
	if generatedAt.Equal(time.Time{}) {
		return ScopeCatalogSnapshot{}, fmt.Errorf("invalid Auth Scope Catalog generated_at: zero value")
	}

	requestable := make([]domain.ScopeName, 0, len(response.GetScopes()))
	previous := ""
	for index, definition := range response.GetScopes() {
		if definition == nil || definition.GetName() == "" {
			return ScopeCatalogSnapshot{}, fmt.Errorf("invalid Auth Scope Catalog scope at index %d", index)
		}
		name := definition.GetName()
		if index > 0 && name <= previous {
			return ScopeCatalogSnapshot{}, fmt.Errorf("invalid Auth Scope Catalog ordering at index %d", index)
		}
		previous = name
		if definition.GetRequestable() {
			requestable = append(requestable, domain.ScopeName(name))
		}
	}

	return ScopeCatalogSnapshot{
		Revision:          port.ScopeCatalogRevision(response.GetRevision()),
		GeneratedAt:       generatedAt,
		RequestableScopes: requestable,
	}, nil
}

var _ ScopeCatalogSnapshotSource = (*GRPCScopeCatalogSnapshotSource)(nil)
