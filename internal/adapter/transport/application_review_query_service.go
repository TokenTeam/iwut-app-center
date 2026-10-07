package transport

import (
	"context"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review_query"
	"google.golang.org/protobuf/types/known/timestamppb"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewusecase "iwut-app-center/internal/review/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationReviewQueryHandler interface {
	List(context.Context, shared.TrustedIdentity, reviewusecase.ListPendingReviewsQuery) (*reviewdomain.PendingReviewPage, error)
	Get(context.Context, shared.TrustedIdentity, string, string, string) (*reviewdomain.ReviewDetail, error)
}
type ApplicationReviewQueryService struct {
	queryv1.UnimplementedApplicationReviewQueryServiceServer
	handler ApplicationReviewQueryHandler
}

func NewApplicationReviewQueryService(h ApplicationReviewQueryHandler) *ApplicationReviewQueryService {
	return &ApplicationReviewQueryService{handler: h}
}

var _ queryv1.ApplicationReviewQueryServiceServer = (*ApplicationReviewQueryService)(nil)
var _ queryv1.ApplicationReviewQueryServiceHTTPServer = (*ApplicationReviewQueryService)(nil)

func (s *ApplicationReviewQueryService) ListPendingApplicationVersionReviews(ctx context.Context, request *queryv1.ListPendingApplicationVersionReviewsRequest) (*queryv1.PendingApplicationVersionReviewPage, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(reviewdomain.ErrReviewerIdentityRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(reviewdomain.ErrInvalidApplicationReviewQuery)
	}
	input := request.GetQuery()
	if input == nil {
		input = &queryv1.ListPendingApplicationVersionReviewsQuery{}
	}
	page, err := s.handler.List(ctx, identity, reviewusecase.ListPendingReviewsQuery{ApplicationID: input.ApplicationId, PageSize: input.GetPageSize(), PageToken: input.GetPageToken()})
	if err != nil {
		return nil, toTransportError(err)
	}
	items := make([]*queryv1.PendingApplicationVersionReview, len(page.Items))
	for i, item := range page.Items {
		items[i] = &queryv1.PendingApplicationVersionReview{ApplicationId: item.ApplicationID, ApplicationName: item.ApplicationName, VersionId: item.VersionID, VersionLabel: item.VersionLabel, ReviewId: item.ReviewID, Attempt: item.Attempt, SubmittedBy: item.SubmittedBy, SubmittedAt: timestamppb.New(item.SubmittedAt), DecisionEligibility: versionEligibilityResponse(item.DecisionEligibility)}
	}
	return &queryv1.PendingApplicationVersionReviewPage{Items: items, NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}, nil
}
func (s *ApplicationReviewQueryService) GetApplicationVersionReview(ctx context.Context, request *queryv1.GetApplicationVersionReviewRequest) (*queryv1.ApplicationVersionReviewDetail, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(reviewdomain.ErrReviewerIdentityRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(reviewdomain.ErrApplicationReviewNotFound)
	}
	d, err := s.handler.Get(ctx, identity, request.GetApplicationId(), request.GetVersionId(), request.GetReviewId())
	if err != nil {
		return nil, toTransportError(err)
	}
	return &queryv1.ApplicationVersionReviewDetail{Application: &queryv1.ReviewApplicationContext{ApplicationId: d.Application.ApplicationID, Name: d.Application.Name, AdminId: d.Application.AdminID, LifecycleStatus: d.Application.LifecycleStatus, PlatformAvailabilityStatus: d.Application.PlatformAvailabilityStatus}, Version: &queryv1.ReviewVersionContext{VersionId: d.Version.VersionID, Sequence: d.Version.Sequence, ReviewStatus: d.Version.ReviewStatus, Revision: d.Version.Revision, CreatedBy: d.Version.CreatedBy}, Review: applicationReviewResource(d.Review), CurrentPolicy: &queryv1.ReviewPolicyContext{Version: d.CurrentPolicy.Version, RequiredCheckIds: append([]string(nil), d.CurrentPolicy.RequiredCheckIDs...)}, DecisionEligibility: versionEligibilityResponse(d.DecisionEligibility), AsOf: timestamppb.New(d.AsOf)}, nil
}
func versionEligibilityResponse(v reviewdomain.DecisionEligibility) *queryv1.ReviewDecisionEligibility {
	out := &queryv1.ReviewDecisionEligibility{Eligible: v.Eligible}
	for _, c := range v.Conflicts {
		switch c {
		case reviewdomain.ConflictCurrentAdmin:
			out.Conflicts = append(out.Conflicts, queryv1.ReviewConflict_CURRENT_ADMIN)
		case reviewdomain.ConflictVersionCreator:
			out.Conflicts = append(out.Conflicts, queryv1.ReviewConflict_VERSION_CREATOR)
		case reviewdomain.ConflictReviewSubmitter:
			out.Conflicts = append(out.Conflicts, queryv1.ReviewConflict_REVIEW_SUBMITTER)
		}
	}
	return out
}
