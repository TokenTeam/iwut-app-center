package usecase

import (
	"context"
	"errors"
	"testing"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

type fakeReviewQueryRepository struct {
	reviewer shared.AuthID
	filter   *shared.ApplicationID
	size     int32
	token    string
	page     *reviewdomain.PendingReviewPage
	detail   *reviewdomain.ReviewDetail
	err      error
}

func (f *fakeReviewQueryRepository) ListPending(_ context.Context, r shared.AuthID, a *shared.ApplicationID, s int32, t string) (*reviewdomain.PendingReviewPage, error) {
	f.reviewer = r
	f.filter = a
	f.size = s
	f.token = t
	return f.page, f.err
}
func (f *fakeReviewQueryRepository) Get(context.Context, shared.AuthID, shared.ApplicationID, reviewdomain.ApplicationVersionID, reviewdomain.ApplicationReviewID) (*reviewdomain.ReviewDetail, error) {
	return f.detail, f.err
}
func TestReviewQuery_BRREV029_BRREV033(t *testing.T) {
	repo := &fakeReviewQueryRepository{page: &reviewdomain.PendingReviewPage{Items: []reviewdomain.PendingReviewSummary{}}}
	q := NewReviewQuery(repo)
	identity := shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{PermissionApplicationVersionReview}}
	page, err := q.List(t.Context(), identity, ListPendingReviewsQuery{})
	if err != nil || page == nil || repo.reviewer != "reviewer" || repo.size != 20 {
		t.Fatalf("page=%#v repo=%#v err=%v", page, repo, err)
	}
	if _, err = q.List(t.Context(), shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{"app.profile.review"}}, ListPendingReviewsQuery{}); !errors.Is(err, reviewdomain.ErrApplicationReviewPermissionRequired) {
		t.Fatalf("permission err=%v", err)
	}
	if _, err = q.List(t.Context(), shared.TrustedIdentity{}, ListPendingReviewsQuery{}); !errors.Is(err, reviewdomain.ErrReviewerIdentityRequired) {
		t.Fatalf("identity err=%v", err)
	}
}
func TestReviewQueryMapsErrors_BRREV034(t *testing.T) {
	for _, tc := range []struct{ source, want error }{{reviewport.ErrInvalidReviewPageToken, reviewdomain.ErrInvalidApplicationReviewPageToken}, {reviewport.ErrReviewQueryNotFound, reviewdomain.ErrApplicationReviewNotFound}, {reviewport.ErrReviewQueryStateInconsistent, reviewdomain.ErrApplicationReviewQueryStateInconsistent}, {errors.New("storage"), reviewdomain.NewInternalError(nil)}} {
		repo := &fakeReviewQueryRepository{err: tc.source}
		q := NewReviewQuery(repo)
		_, err := q.List(t.Context(), shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{PermissionApplicationVersionReview}}, ListPendingReviewsQuery{})
		if !errors.Is(err, tc.want) {
			t.Fatalf("source=%v err=%v want=%v", tc.source, err, tc.want)
		}
	}
}
