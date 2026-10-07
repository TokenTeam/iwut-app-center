package transport

import (
	"context"
	"testing"
	"time"

	managementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management"
	managementdomain "iwut-app-center/internal/management/domain"
	managementusecase "iwut-app-center/internal/management/usecase"
	"iwut-app-center/internal/shared"
)

type fakeManagementHandler struct {
	identity shared.AuthenticatedUserIdentity
	query    managementusecase.ListQuery
}

func (f *fakeManagementHandler) List(_ context.Context, i shared.AuthenticatedUserIdentity, q managementusecase.ListQuery) (*managementdomain.Page, error) {
	f.identity = i
	f.query = q
	return &managementdomain.Page{AsOf: time.Unix(1, 0), Items: []managementdomain.Summary{{Application: managementdomain.ApplicationCore{ApplicationID: "01890f47-0000-7000-8000-000000000001", Name: "sample", AdminID: i.AuthID.String(), CreatedAt: time.Unix(1, 0), OwnershipRevision: 1, LifecycleStatus: "ACTIVE", LifecycleRevision: 1, PlatformAvailabilityStatus: "AVAILABLE", PlatformAvailabilityRevision: 1}}}}, nil
}
func (f *fakeManagementHandler) Get(_ context.Context, i shared.AuthenticatedUserIdentity, id string) (*managementdomain.Detail, error) {
	f.identity = i
	return &managementdomain.Detail{Application: managementdomain.ApplicationCore{ApplicationID: id, Name: "sample", AdminID: i.AuthID.String(), CreatedAt: time.Unix(1, 0), OwnershipRevision: 1, LifecycleStatus: "ACTIVE", LifecycleRevision: 1, PlatformAvailabilityStatus: "AVAILABLE", PlatformAvailabilityRevision: 1}, FilterState: managementdomain.FilterState{SchemaVersion: "profile-filter-v1", Mode: "ALLOW_ALL"}, AsOf: time.Unix(2, 0)}, nil
}
func TestApplicationManagementService_BRAPP038(t *testing.T) {
	h := &fakeManagementHandler{}
	s := NewApplicationManagementQueryService(h)
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "admin"})
	page, err := s.ListMyApplications(ctx, &managementv1.ListMyApplicationsRequest{Query: &managementv1.ListMyApplicationsQuery{PageSize: 7}})
	if err != nil || len(page.GetItems()) != 1 || h.query.PageSize != 7 || h.identity.AuthID != "admin" {
		t.Fatalf("page=%#v handler=%#v err=%v", page, h, err)
	}
	detail, err := s.GetMyApplication(ctx, &managementv1.GetMyApplicationRequest{ApplicationId: "01890f47-0000-7000-8000-000000000001"})
	if err != nil || detail.GetFilterState().GetMode() != "ALLOW_ALL" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
}
