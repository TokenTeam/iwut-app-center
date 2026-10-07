package usecase

import (
	"context"
	"errors"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
	"slices"
)

const ReviewQueryDefaultPageSize int32 = 20
const ReviewQueryMaxPageSize int32 = 100
const ReviewQueryMaxTokenBytes = 4096

type ListPendingReviewsQuery struct {
	ApplicationID *string
	PageSize      int32
	PageToken     string
}
type ReviewQuery struct {
	repository reviewport.ReviewQueryRepository
}

func NewReviewQuery(repository reviewport.ReviewQueryRepository) *ReviewQuery {
	return &ReviewQuery{repository: repository}
}
func (q *ReviewQuery) authorize(identity shared.TrustedIdentity) error {
	if !identity.AuthID.IsValid() {
		return reviewdomain.ErrReviewerIdentityRequired
	}
	if !slices.Contains(identity.Permissions, PermissionApplicationVersionReview) {
		return reviewdomain.ErrApplicationReviewPermissionRequired
	}
	return nil
}
func (q *ReviewQuery) List(ctx context.Context, identity shared.TrustedIdentity, input ListPendingReviewsQuery) (*reviewdomain.PendingReviewPage, error) {
	if err := q.authorize(identity); err != nil {
		return nil, err
	}
	size := input.PageSize
	if size == 0 {
		size = ReviewQueryDefaultPageSize
	}
	if size < 1 || size > ReviewQueryMaxPageSize {
		return nil, reviewdomain.ErrInvalidApplicationReviewQuery
	}
	if len(input.PageToken) > ReviewQueryMaxTokenBytes {
		return nil, reviewdomain.ErrInvalidApplicationReviewPageToken
	}
	var appID *shared.ApplicationID
	if input.ApplicationID != nil {
		id, ok := shared.ParseApplicationID(*input.ApplicationID)
		if !ok {
			return nil, reviewdomain.ErrInvalidApplicationReviewQuery
		}
		appID = &id
	}
	if q == nil || q.repository == nil {
		return nil, reviewdomain.NewInternalError(nil)
	}
	page, err := q.repository.ListPending(ctx, identity.AuthID, appID, size, input.PageToken)
	if err != nil {
		return nil, mapReviewQueryError(err)
	}
	if page == nil {
		return nil, reviewdomain.ErrApplicationReviewQueryStateInconsistent
	}
	return page, nil
}
func (q *ReviewQuery) Get(ctx context.Context, identity shared.TrustedIdentity, applicationID, versionID, reviewID string) (*reviewdomain.ReviewDetail, error) {
	if err := q.authorize(identity); err != nil {
		return nil, err
	}
	app, ok := shared.ParseApplicationID(applicationID)
	version := reviewdomain.ApplicationVersionID(versionID)
	review := reviewdomain.ApplicationReviewID(reviewID)
	if !ok || !version.IsValid() || !review.IsValid() {
		return nil, reviewdomain.ErrApplicationReviewNotFound
	}
	if q == nil || q.repository == nil {
		return nil, reviewdomain.NewInternalError(nil)
	}
	detail, err := q.repository.Get(ctx, identity.AuthID, app, version, review)
	if err != nil {
		return nil, mapReviewQueryError(err)
	}
	if detail == nil || detail.Review == nil {
		return nil, reviewdomain.ErrApplicationReviewQueryStateInconsistent
	}
	return detail, nil
}
func mapReviewQueryError(err error) error {
	switch {
	case errors.Is(err, reviewport.ErrInvalidReviewPageToken):
		return reviewdomain.ErrInvalidApplicationReviewPageToken
	case errors.Is(err, reviewport.ErrReviewQueryNotFound):
		return reviewdomain.ErrApplicationReviewNotFound
	case errors.Is(err, reviewport.ErrReviewQueryStateInconsistent):
		return reviewdomain.ErrApplicationReviewQueryStateInconsistent
	default:
		return reviewdomain.NewInternalError(err)
	}
}
