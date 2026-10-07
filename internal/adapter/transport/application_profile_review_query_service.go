package transport

import (
	"context"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review_query"
	"google.golang.org/protobuf/types/known/timestamppb"

	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationProfileReviewQueryHandler interface {
	List(context.Context, shared.TrustedIdentity, profileusecase.ListPendingProfileReviewsQuery) (*profiledomain.PendingProfileReviewPage, error)
	Get(context.Context, shared.TrustedIdentity, string, string, string) (*profiledomain.ProfileReviewDetail, error)
}

type ApplicationProfileReviewQueryService struct {
	queryv1.UnimplementedApplicationProfileReviewQueryServiceServer
	handler ApplicationProfileReviewQueryHandler
}

func NewApplicationProfileReviewQueryService(handler ApplicationProfileReviewQueryHandler) *ApplicationProfileReviewQueryService {
	return &ApplicationProfileReviewQueryService{handler: handler}
}

var _ queryv1.ApplicationProfileReviewQueryServiceServer = (*ApplicationProfileReviewQueryService)(nil)
var _ queryv1.ApplicationProfileReviewQueryServiceHTTPServer = (*ApplicationProfileReviewQueryService)(nil)

func (s *ApplicationProfileReviewQueryService) ListPendingApplicationProfileReviews(ctx context.Context, request *queryv1.ListPendingApplicationProfileReviewsRequest) (*queryv1.PendingApplicationProfileReviewPage, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(profiledomain.ErrReviewerIdentityRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationProfileReviewQuery)
	}
	input := request.GetQuery()
	if input == nil {
		input = &queryv1.ListPendingApplicationProfileReviewsQuery{}
	}
	page, err := s.handler.List(ctx, identity, profileusecase.ListPendingProfileReviewsQuery{ApplicationID: input.ApplicationId, PageSize: input.GetPageSize(), PageToken: input.GetPageToken()})
	if err != nil {
		return nil, toTransportError(err)
	}
	items := make([]*queryv1.PendingApplicationProfileReview, len(page.Items))
	for index, item := range page.Items {
		items[index] = &queryv1.PendingApplicationProfileReview{
			ApplicationId: item.ApplicationID, ApplicationName: item.ApplicationName,
			ProfileRevisionId: item.ProfileRevisionID, Sequence: item.Sequence, DisplayName: item.DisplayName,
			ProfileReviewId: item.ProfileReviewID, Attempt: item.Attempt, SubmittedAt: timestamppb.New(item.SubmittedAt),
			DecisionEligibility: profileEligibilityResponse(item.DecisionEligibility),
		}
	}
	return &queryv1.PendingApplicationProfileReviewPage{Items: items, NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}, nil
}

func (s *ApplicationProfileReviewQueryService) GetApplicationProfileReview(ctx context.Context, request *queryv1.GetApplicationProfileReviewRequest) (*queryv1.ApplicationProfileReviewDetail, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(profiledomain.ErrReviewerIdentityRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(profiledomain.ErrApplicationProfileReviewNotFound)
	}
	detail, err := s.handler.Get(ctx, identity, request.GetApplicationId(), request.GetProfileRevisionId(), request.GetProfileReviewId())
	if err != nil {
		return nil, toTransportError(err)
	}
	return &queryv1.ApplicationProfileReviewDetail{
		Application:         &queryv1.ProfileReviewApplicationContext{ApplicationId: detail.Application.ApplicationID, Name: detail.Application.Name, AdminId: detail.Application.AdminID, LifecycleStatus: detail.Application.LifecycleStatus, PlatformAvailabilityStatus: detail.Application.PlatformAvailabilityStatus},
		ProfileRevision:     &queryv1.ProfileRevisionReviewContext{ProfileRevisionId: detail.ProfileRevision.ProfileRevisionID, Sequence: detail.ProfileRevision.Sequence, ReviewStatus: detail.ProfileRevision.ReviewStatus, Revision: detail.ProfileRevision.Revision, CreatedBy: detail.ProfileRevision.CreatedBy},
		Review:              applicationProfileReviewResponse(detail.Review),
		CurrentPolicy:       &queryv1.ProfileReviewPolicyContext{Version: detail.CurrentPolicy.Version, RequiredCheckIds: append([]string(nil), detail.CurrentPolicy.RequiredCheckIDs...)},
		DecisionEligibility: profileEligibilityResponse(detail.DecisionEligibility), AsOf: timestamppb.New(detail.AsOf),
	}, nil
}

func profileEligibilityResponse(value profiledomain.ProfileDecisionEligibility) *queryv1.ProfileReviewDecisionEligibility {
	response := &queryv1.ProfileReviewDecisionEligibility{Eligible: value.Eligible}
	for _, conflict := range value.Conflicts {
		switch conflict {
		case profiledomain.ProfileConflictCurrentAdmin:
			response.Conflicts = append(response.Conflicts, queryv1.ProfileReviewConflict_CURRENT_ADMIN)
		case profiledomain.ProfileConflictRevisionCreator:
			response.Conflicts = append(response.Conflicts, queryv1.ProfileReviewConflict_REVISION_CREATOR)
		case profiledomain.ProfileConflictReviewSubmitter:
			response.Conflicts = append(response.Conflicts, queryv1.ProfileReviewConflict_REVIEW_SUBMITTER)
		}
	}
	return response
}
