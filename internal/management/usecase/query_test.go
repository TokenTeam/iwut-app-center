package usecase

import (
	"context"
	"errors"
	"testing"

	"iwut-app-center/internal/management/domain"
	"iwut-app-center/internal/management/port"
	"iwut-app-center/internal/shared"
)

type fakeRepository struct {
	lifecycle, availability []string
	admin                   shared.AuthID
	size                    int32
	token                   string
	page                    *domain.Page
	detail                  *domain.Detail
	err                     error
}

func (f *fakeRepository) List(_ context.Context, a shared.AuthID, l, p []string, s int32, t string) (*domain.Page, error) {
	f.admin = a
	f.lifecycle = l
	f.availability = p
	f.size = s
	f.token = t
	return f.page, f.err
}
func (f *fakeRepository) Get(_ context.Context, a shared.AuthID, _ shared.ApplicationID) (*domain.Detail, error) {
	f.admin = a
	return f.detail, f.err
}

func TestQueryList_BRAPP038_BRAPP040(t *testing.T) {
	repo := &fakeRepository{page: &domain.Page{Items: []domain.Summary{}}}
	q := NewQuery(repo)
	page, err := q.List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, ListQuery{})
	if err != nil || page == nil || repo.admin != "admin" || repo.size != 20 || len(repo.lifecycle) != 2 || repo.lifecycle[0] != "ACTIVE" || repo.lifecycle[1] != "CLOSING" {
		t.Fatalf("page=%#v repo=%#v err=%v", page, repo, err)
	}
	for _, input := range []ListQuery{{LifecycleStatuses: []string{"BAD"}}, {LifecycleStatuses: []string{"ACTIVE", "ACTIVE"}}, {PlatformAvailabilityStatuses: []string{"BAD"}}, {PageSize: 101}} {
		if _, err = q.List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, input); !errors.Is(err, domain.ErrInvalidRequest) {
			t.Fatalf("input=%#v err=%v", input, err)
		}
	}
	if _, err = q.List(t.Context(), shared.AuthenticatedUserIdentity{}, ListQuery{}); !errors.Is(err, domain.ErrAuthenticatedUserRequired) {
		t.Fatalf("identity err=%v", err)
	}
}
func TestQueryMapsRepositoryErrors_BRAPP042(t *testing.T) {
	for _, tc := range []struct{ source, want error }{{port.ErrInvalidPageToken, domain.ErrInvalidPageToken}, {port.ErrNotFound, domain.ErrNotFound}, {port.ErrStateInconsistent, domain.ErrStateInconsistent}, {errors.New("storage"), domain.NewInternalError(nil)}} {
		repo := &fakeRepository{err: tc.source}
		q := NewQuery(repo)
		_, err := q.List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, ListQuery{})
		if !errors.Is(err, tc.want) {
			t.Fatalf("source=%v err=%v want=%v", tc.source, err, tc.want)
		}
	}
}
