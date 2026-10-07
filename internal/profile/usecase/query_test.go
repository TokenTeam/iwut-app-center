package usecase

import (
	"context"
	"errors"
	"testing"

	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type fakeProfileReviewQueryRepository struct {
	reviewer shared.AuthID
	filter   *shared.ApplicationID
	size     int32
	token    string
	page     *profiledomain.PendingProfileReviewPage
	detail   *profiledomain.ProfileReviewDetail
	err      error
}

func (f *fakeProfileReviewQueryRepository) ListPending(_ context.Context, reviewer shared.AuthID, filter *shared.ApplicationID, size int32, token string) (*profiledomain.PendingProfileReviewPage, error) {
	f.reviewer, f.filter, f.size, f.token = reviewer, filter, size, token
	return f.page, f.err
}

func (f *fakeProfileReviewQueryRepository) Get(context.Context, shared.AuthID, shared.ApplicationID, profiledomain.ApplicationProfileRevisionID, profiledomain.ApplicationProfileReviewID) (*profiledomain.ProfileReviewDetail, error) {
	return f.detail, f.err
}

func TestProfileReviewQuery_BRPRF041_BRPRF045(t *testing.T) {
	repository := &fakeProfileReviewQueryRepository{page: &profiledomain.PendingProfileReviewPage{Items: []profiledomain.PendingProfileReviewSummary{}}}
	query := NewProfileReviewQuery(repository)
	identity := shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{profiledomain.ProfileReviewPermission}}
	page, err := query.List(t.Context(), identity, ListPendingProfileReviewsQuery{})
	if err != nil || page == nil || repository.reviewer != "reviewer" || repository.size != ProfileReviewQueryDefaultPageSize {
		t.Fatalf("page=%#v repository=%#v err=%v", page, repository, err)
	}
	if _, err = query.List(t.Context(), shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{"app.version.review"}}, ListPendingProfileReviewsQuery{}); !errors.Is(err, profiledomain.ErrApplicationProfileReviewPermissionRequired) {
		t.Fatalf("wrong permission err=%v", err)
	}
	if _, err = query.List(t.Context(), shared.TrustedIdentity{}, ListPendingProfileReviewsQuery{}); !errors.Is(err, profiledomain.ErrReviewerIdentityRequired) {
		t.Fatalf("missing identity err=%v", err)
	}
	if _, err = query.List(t.Context(), identity, ListPendingProfileReviewsQuery{PageSize: ProfileReviewQueryMaxPageSize + 1}); !errors.Is(err, profiledomain.ErrInvalidApplicationProfileReviewQuery) {
		t.Fatalf("page size err=%v", err)
	}
}

func TestProfileReviewQueryMapsRepositoryErrors_BRPRF046(t *testing.T) {
	identity := shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{profiledomain.ProfileReviewPermission}}
	for _, testCase := range []struct{ source, want error }{
		{profileport.ErrInvalidProfileReviewPageToken, profiledomain.ErrInvalidApplicationProfileReviewPageToken},
		{profileport.ErrProfileReviewQueryNotFound, profiledomain.ErrApplicationProfileReviewNotFound},
		{profileport.ErrProfileReviewQueryStateInconsistent, profiledomain.ErrApplicationProfileReviewStateInconsistent},
		{errors.New("storage"), profiledomain.NewInternalError(nil)},
	} {
		repository := &fakeProfileReviewQueryRepository{err: testCase.source}
		_, err := NewProfileReviewQuery(repository).List(t.Context(), identity, ListPendingProfileReviewsQuery{})
		if !errors.Is(err, testCase.want) {
			t.Fatalf("source=%v err=%v want=%v", testCase.source, err, testCase.want)
		}
	}
}
