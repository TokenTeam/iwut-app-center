package auth

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	scopecatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/scope_catalog"
	"iwut-app-center/internal/version/domain"
)

func TestScopeCatalogSnapshotFromProto_ContractProjection(t *testing.T) {
	t.Parallel()
	generatedAt := time.Date(2026, time.September, 21, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	snapshot, err := scopeCatalogSnapshotFromProto(&scopecatalogv1.GetScopeCatalogSnapshotResponse{
		Revision:    17,
		GeneratedAt: timestamppb.New(generatedAt),
		Scopes: []*scopecatalogv1.ScopeDefinition{
			{Name: "legacy.profile", Requestable: false},
			{Name: "profile.basic", Requestable: true},
			{Name: "schedule.read", Requestable: true},
		},
	})
	if err != nil {
		t.Fatalf("scopeCatalogSnapshotFromProto() error = %v", err)
	}
	if snapshot.Revision != 17 || !snapshot.GeneratedAt.Equal(generatedAt.UTC()) {
		t.Fatalf("snapshot metadata = (%d, %v)", snapshot.Revision, snapshot.GeneratedAt)
	}
	if !reflect.DeepEqual(snapshot.RequestableScopes, []domain.ScopeName{"profile.basic", "schedule.read"}) {
		t.Fatalf("requestable scopes = %v", snapshot.RequestableScopes)
	}
}

func TestScopeCatalogSnapshotFromProto_RejectsContractViolations(t *testing.T) {
	t.Parallel()
	validTime := timestamppb.New(time.Now())
	tests := []struct {
		name     string
		response *scopecatalogv1.GetScopeCatalogSnapshotResponse
	}{
		{name: "nil response"},
		{name: "non-positive revision", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{GeneratedAt: validTime}},
		{name: "missing generated at", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1}},
		{name: "invalid generated at", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1, GeneratedAt: &timestamppb.Timestamp{Seconds: 253402300800}}},
		{name: "empty name", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1, GeneratedAt: validTime, Scopes: []*scopecatalogv1.ScopeDefinition{{}}}},
		{name: "duplicate name", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1, GeneratedAt: validTime, Scopes: []*scopecatalogv1.ScopeDefinition{{Name: "a"}, {Name: "a"}}}},
		{name: "unstable order", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1, GeneratedAt: validTime, Scopes: []*scopecatalogv1.ScopeDefinition{{Name: "b"}, {Name: "a"}}}},
		{name: "nil definition", response: &scopecatalogv1.GetScopeCatalogSnapshotResponse{Revision: 1, GeneratedAt: validTime, Scopes: []*scopecatalogv1.ScopeDefinition{nil}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := scopeCatalogSnapshotFromProto(test.response); err == nil {
				t.Fatal("scopeCatalogSnapshotFromProto() error = nil")
			}
		})
	}
}
