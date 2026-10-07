package usecase

import (
	"context"
	"errors"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"slices"
)

const ProfileReviewQueryDefaultPageSize int32 = 20
const ProfileReviewQueryMaxPageSize int32 = 100
const ProfileReviewQueryMaxTokenBytes = 4096

type ListPendingProfileReviewsQuery struct {
	ApplicationID *string
	PageSize      int32
	PageToken     string
}
type ProfileReviewQuery struct {
	repository profileport.ProfileReviewQueryRepository
}

func NewProfileReviewQuery(repository profileport.ProfileReviewQueryRepository) *ProfileReviewQuery {
	return &ProfileReviewQuery{repository: repository}
}
func (q *ProfileReviewQuery) authorize(i shared.TrustedIdentity) error {
	if !i.AuthID.IsValid() {
		return profiledomain.ErrReviewerIdentityRequired
	}
	if !slices.Contains(i.Permissions, profiledomain.ProfileReviewPermission) {
		return profiledomain.ErrApplicationProfileReviewPermissionRequired
	}
	return nil
}
func (q *ProfileReviewQuery) List(ctx context.Context, i shared.TrustedIdentity, in ListPendingProfileReviewsQuery) (*profiledomain.PendingProfileReviewPage, error) {
	if err := q.authorize(i); err != nil {
		return nil, err
	}
	size := in.PageSize
	if size == 0 {
		size = ProfileReviewQueryDefaultPageSize
	}
	if size < 1 || size > ProfileReviewQueryMaxPageSize {
		return nil, profiledomain.ErrInvalidApplicationProfileReviewQuery
	}
	if len(in.PageToken) > ProfileReviewQueryMaxTokenBytes {
		return nil, profiledomain.ErrInvalidApplicationProfileReviewPageToken
	}
	var app *shared.ApplicationID
	if in.ApplicationID != nil {
		id, ok := shared.ParseApplicationID(*in.ApplicationID)
		if !ok {
			return nil, profiledomain.ErrInvalidApplicationProfileReviewQuery
		}
		app = &id
	}
	if q == nil || q.repository == nil {
		return nil, profiledomain.NewInternalError(nil)
	}
	page, err := q.repository.ListPending(ctx, i.AuthID, app, size, in.PageToken)
	if err != nil {
		return nil, mapProfileQueryError(err)
	}
	if page == nil {
		return nil, profiledomain.ErrApplicationProfileReviewStateInconsistent
	}
	return page, nil
}
func (q *ProfileReviewQuery) Get(ctx context.Context, i shared.TrustedIdentity, applicationID, revisionID, reviewID string) (*profiledomain.ProfileReviewDetail, error) {
	if err := q.authorize(i); err != nil {
		return nil, err
	}
	app, ok := shared.ParseApplicationID(applicationID)
	revision := profiledomain.ApplicationProfileRevisionID(revisionID)
	review := profiledomain.ApplicationProfileReviewID(reviewID)
	if !ok || !revision.IsValid() || !review.IsValid() {
		return nil, profiledomain.ErrApplicationProfileReviewNotFound
	}
	if q == nil || q.repository == nil {
		return nil, profiledomain.NewInternalError(nil)
	}
	detail, err := q.repository.Get(ctx, i.AuthID, app, revision, review)
	if err != nil {
		return nil, mapProfileQueryError(err)
	}
	if detail == nil || detail.Review == nil {
		return nil, profiledomain.ErrApplicationProfileReviewStateInconsistent
	}
	return detail, nil
}
func mapProfileQueryError(err error) error {
	switch {
	case errors.Is(err, profileport.ErrInvalidProfileReviewPageToken):
		return profiledomain.ErrInvalidApplicationProfileReviewPageToken
	case errors.Is(err, profileport.ErrProfileReviewQueryNotFound):
		return profiledomain.ErrApplicationProfileReviewNotFound
	case errors.Is(err, profileport.ErrProfileReviewQueryStateInconsistent):
		return profiledomain.ErrApplicationProfileReviewStateInconsistent
	default:
		return profiledomain.NewInternalError(err)
	}
}
