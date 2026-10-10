package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

const (
	queryApplicationID = "01890f47-0000-7000-8000-000000003401"
	queryVersionID     = "01890f47-0000-7000-8000-000000003402"
)

type fakeManagementQueryRepository struct {
	page      *domain.OwnedVersionPage
	detail    *domain.OwnedVersionDetail
	err       error
	adminID   shared.AuthID
	appID     shared.ApplicationID
	status    *domain.ReviewStatus
	pageSize  int32
	pageToken string
	versionID domain.ApplicationVersionID
}

func (f *fakeManagementQueryRepository) List(_ context.Context, admin shared.AuthID, app shared.ApplicationID, status *domain.ReviewStatus, size int32, token string) (*domain.OwnedVersionPage, error) {
	f.adminID, f.appID, f.status, f.pageSize, f.pageToken = admin, app, status, size, token
	return f.page, f.err
}
func (f *fakeManagementQueryRepository) Get(_ context.Context, admin shared.AuthID, app shared.ApplicationID, version domain.ApplicationVersionID) (*domain.OwnedVersionDetail, error) {
	f.adminID, f.appID, f.versionID = admin, app, version
	return f.detail, f.err
}

func TestManagementQuery_BRVER019_BRVER020DoesNotRequireDeveloperApproval(t *testing.T) {
	repository := &fakeManagementQueryRepository{page: &domain.OwnedVersionPage{Items: []domain.OwnedVersion{}, AsOf: time.Now()}}
	query := NewManagementQuery(repository)
	status := "REJECTED"
	page, err := query.List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "current-admin"}, ListApplicationVersionsQuery{ApplicationID: queryApplicationID, ReviewStatus: &status})
	if err != nil || page == nil || repository.adminID != "current-admin" || repository.pageSize != 20 || repository.status == nil || *repository.status != domain.ReviewStatusRejected {
		t.Fatalf("List() = (%#v, %v), repository=%#v", page, err, repository)
	}
}

func TestManagementQuery_BRVER022ValidatesQueryAndToken(t *testing.T) {
	for _, test := range []struct {
		name  string
		input ListApplicationVersionsQuery
		want  error
	}{
		{name: "invalid application", input: ListApplicationVersionsQuery{ApplicationID: "bad"}, want: domain.ErrInvalidApplicationVersionQuery},
		{name: "invalid status", input: ListApplicationVersionsQuery{ApplicationID: queryApplicationID, ReviewStatus: stringPointer("REVOKED")}, want: domain.ErrInvalidApplicationVersionQuery},
		{name: "invalid size", input: ListApplicationVersionsQuery{ApplicationID: queryApplicationID, PageSize: 101}, want: domain.ErrInvalidApplicationVersionQuery},
		{name: "oversize token", input: ListApplicationVersionsQuery{ApplicationID: queryApplicationID, PageToken: string(make([]byte, 4097))}, want: domain.ErrInvalidApplicationVersionPageToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewManagementQuery(&fakeManagementQueryRepository{}).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf("List() error = %v, want %v", err, test.want)
			}
		})
	}
	repository := &fakeManagementQueryRepository{err: port.ErrInvalidManagementPageToken}
	_, err := NewManagementQuery(repository).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, ListApplicationVersionsQuery{ApplicationID: queryApplicationID, PageToken: "opaque"})
	if !errors.Is(err, domain.ErrInvalidApplicationVersionPageToken) {
		t.Fatalf("List() error = %v, want invalid page token", err)
	}
}

func TestManagementQuery_BRVER019_BRVER024MapsPrivateReadFailures(t *testing.T) {
	for _, test := range []struct {
		portErr error
		want    error
	}{
		{port.ErrManagementNotFound, domain.ErrApplicationVersionManagementNotFound},
		{port.ErrManagementStateInconsistent, domain.ErrApplicationVersionManagementStateInconsistent},
	} {
		repository := &fakeManagementQueryRepository{err: test.portErr}
		_, err := NewManagementQuery(repository).Get(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, queryApplicationID, queryVersionID)
		if !errors.Is(err, test.want) {
			t.Fatalf("Get() error = %v, want %v", err, test.want)
		}
	}
}

func TestManagementQuery_BRVER021_BRVER025ReturnsCurrentDetail(t *testing.T) {
	detail := &domain.OwnedVersionDetail{Version: domain.OwnedVersion{ApplicationID: queryApplicationID, VersionID: queryVersionID, ReviewStatus: "REJECTED", LatestReview: &domain.OwnedLatestReview{ReviewID: "01890f47-0000-7000-8000-000000003403", Decision: &domain.OwnedReviewDecision{Outcome: "REJECTED", Reason: stringPointer("fix redirect")}}}, AsOf: time.Now()}
	repository := &fakeManagementQueryRepository{detail: detail}
	got, err := NewManagementQuery(repository).Get(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, queryApplicationID, queryVersionID)
	if err != nil || got != detail || got.Version.LatestReview.Decision.Reason == nil || *got.Version.LatestReview.Decision.Reason != "fix redirect" {
		t.Fatalf("Get() = (%#v, %v)", got, err)
	}
}

func TestManagementQuery_BRVER019RequiresTrustedUser(t *testing.T) {
	_, err := NewManagementQuery(&fakeManagementQueryRepository{}).List(t.Context(), shared.AuthenticatedUserIdentity{}, ListApplicationVersionsQuery{ApplicationID: queryApplicationID})
	if !errors.Is(err, domain.ErrUserIdentityRequired) {
		t.Fatalf("List() error = %v, want user identity required", err)
	}
}

func stringPointer(value string) *string { return &value }
