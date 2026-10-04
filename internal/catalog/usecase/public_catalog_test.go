package usecase

import (
	"context"
	"errors"
	"testing"

	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

type publicCatalogRepositoryFake struct {
	page         *domain.PublicApplicationCatalogPage
	item         *domain.PublicApplicationCatalogItem
	err          error
	major        int32
	pageSize     int32
	token        string
	capabilities []domain.CapabilityName
}

func (f *publicCatalogRepositoryFake) ListPublic(_ context.Context, _ shared.AuthID, major int32, capabilities []domain.CapabilityName, pageSize int32, token string) (*domain.PublicApplicationCatalogPage, error) {
	f.major = major
	f.pageSize = pageSize
	f.token = token
	f.capabilities = capabilities
	return f.page, f.err
}
func (f *publicCatalogRepositoryFake) GetPublic(_ context.Context, _ shared.ApplicationID, _ shared.AuthID, major int32, capabilities []domain.CapabilityName) (*domain.PublicApplicationCatalogItem, error) {
	f.major = major
	f.capabilities = capabilities
	return f.item, f.err
}

func TestBR_CAT_002_007_PublicCatalogUseCaseValidationAndNormalization(t *testing.T) {
	page, _ := domain.NewPublicApplicationCatalogPage(nil, "")
	fake := &publicCatalogRepositoryFake{page: page}
	handler := NewPublicCatalog(fake)
	result, err := handler.List(t.Context(), shared.AuthenticatedUserIdentity{}, ListPublicApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 4, HostCapabilities: []string{"camera.read.v1", "camera.read.v1"}}})
	if err != nil || result != page || fake.pageSize != DefaultCatalogPageSize || fake.major != 4 || len(fake.capabilities) != 1 {
		t.Fatalf("result=%#v err=%v fake=%#v", result, err, fake)
	}
	for _, test := range []struct {
		name  string
		query ListPublicApplicationsQuery
		want  error
	}{{"major", ListPublicApplicationsQuery{Runtime: CatalogRuntimeQuery{}}, domain.ErrInvalidHostRPCAPIMajor}, {"capability", ListPublicApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1, HostCapabilities: []string{"bad"}}}, domain.ErrInvalidHostCapabilities}, {"page size", ListPublicApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}, PageSize: 101}, domain.ErrInvalidPageSize}, {"token", ListPublicApplicationsQuery{Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}, PageToken: string(make([]byte, MaxCatalogPageTokenBytes+1))}, domain.ErrInvalidPageToken}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := handler.List(t.Context(), shared.AuthenticatedUserIdentity{}, test.query); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestBR_CAT_008_PublicCatalogMapsRepositoryErrors(t *testing.T) {
	applicationID := "01890f47-0000-7000-8000-000000000001"
	for _, test := range []struct{ source, want error }{{port.ErrInvalidPageToken, domain.ErrInvalidPageToken}, {port.ErrPublicApplicationNotFound, domain.ErrPublicApplicationNotFound}, {port.ErrApplicationCatalogStateInconsistent, domain.ErrApplicationCatalogStateInconsistent}, {errors.New("storage"), domain.ErrInternal}} {
		t.Run(test.want.Error(), func(t *testing.T) {
			handler := NewPublicCatalog(&publicCatalogRepositoryFake{err: test.source})
			_, err := handler.Get(t.Context(), shared.AuthenticatedUserIdentity{}, GetPublicApplicationQuery{ApplicationID: applicationID, Runtime: CatalogRuntimeQuery{HostRPCAPIMajor: 1}})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}
