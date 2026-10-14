package transport

import (
	"context"
	"errors"
	"net/http"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	pu "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

const (
	ReasonProfileManagementNotFound          = "ERROR_REASON_APPLICATION_PROFILE_MANAGEMENT_NOT_FOUND"
	ReasonInvalidProfileManagementQuery      = "ERROR_REASON_INVALID_APPLICATION_PROFILE_MANAGEMENT_QUERY"
	ReasonInvalidProfileManagementPageToken  = "ERROR_REASON_INVALID_APPLICATION_PROFILE_MANAGEMENT_PAGE_TOKEN"
	ReasonProfileManagementStateInconsistent = "ERROR_REASON_APPLICATION_PROFILE_MANAGEMENT_STATE_INCONSISTENT"
	ReasonProfileManagementUnavailable       = "ERROR_REASON_APPLICATION_PROFILE_MANAGEMENT_UNAVAILABLE"
	ReasonProfileManagementResponseTooLarge  = "ERROR_REASON_APPLICATION_PROFILE_MANAGEMENT_RESPONSE_TOO_LARGE"
)

type ApplicationProfileManagementQueryHandler interface {
	List(context.Context, shared.AuthenticatedUserIdentity, pu.ListProfileRevisionsQuery) (*pd.ManagedProfilePage, error)
	Get(context.Context, shared.AuthenticatedUserIdentity, pu.GetProfileRevisionQuery) (*pd.ManagedProfileDetail, error)
}

func NewApplicationProfileRevisionServiceWithQuery(create CreateApplicationProfileRevisionHandler, update UpdateDraftApplicationProfileRevisionHandler, query ApplicationProfileManagementQueryHandler) *ApplicationProfileRevisionService {
	s := NewApplicationProfileRevisionService(create, update)
	s.queryHandler = query
	return s
}
func profileManagementTransportError(ctx context.Context, err error) error {
	if isHTTP(ctx) && errors.Is(err, pd.ErrProfileManagementResponseTooLarge) {
		return kratoserrors.New(http.StatusRequestEntityTooLarge, ReasonProfileManagementResponseTooLarge, "application profile management response is too large")
	}
	return toTransportError(err)
}
func (s *ApplicationProfileRevisionService) ListApplicationProfileRevisions(ctx context.Context, in *pb.ListApplicationProfileRevisionsRequest) (*pb.OwnedApplicationProfileRevisionPage, error) {
	setPrivateNoStore(ctx)
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(pd.ErrUserIdentityRequired)
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 {
		return nil, toTransportError(pd.ErrInvalidProfileManagementQuery)
	}
	if s == nil || s.queryHandler == nil {
		return nil, toTransportError(pd.ErrProfileManagementUnavailable)
	}
	page, err := s.queryHandler.List(ctx, identity, pu.ListProfileRevisionsQuery{ApplicationID: in.GetApplicationId(), ReviewStatus: in.ReviewStatus, PageSize: in.GetPageSize(), PageToken: in.GetPageToken()})
	if err != nil {
		return nil, profileManagementTransportError(ctx, err)
	}
	if page == nil {
		return nil, toTransportError(pd.ErrProfileManagementStateInconsistent)
	}
	out := profileManagementPageResponse(page)
	if n, err := profileManagementWireSize(out); err != nil || n > pd.ProfileManagementResponseBudget {
		if err != nil {
			return nil, toTransportError(pd.ErrProfileManagementStateInconsistent)
		}
		return nil, profileManagementTransportError(ctx, pd.ErrProfileManagementResponseTooLarge)
	}
	return out, nil
}
func (s *ApplicationProfileRevisionService) GetApplicationProfileRevision(ctx context.Context, in *pb.GetApplicationProfileRevisionRequest) (*pb.OwnedApplicationProfileRevisionDetail, error) {
	setPrivateNoStore(ctx)
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(pd.ErrUserIdentityRequired)
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 {
		return nil, toTransportError(pd.ErrInvalidProfileManagementQuery)
	}
	if s == nil || s.queryHandler == nil {
		return nil, toTransportError(pd.ErrProfileManagementUnavailable)
	}
	detail, err := s.queryHandler.Get(ctx, identity, pu.GetProfileRevisionQuery{ApplicationID: in.GetApplicationId(), ProfileRevisionID: in.GetProfileRevisionId(), ReviewPageSize: in.GetReviewPageSize(), ReviewPageToken: in.GetReviewPageToken()})
	if err != nil {
		return nil, profileManagementTransportError(ctx, err)
	}
	if detail == nil {
		return nil, toTransportError(pd.ErrProfileManagementStateInconsistent)
	}
	out := profileManagementDetailResponse(detail)
	if n, err := profileManagementWireSize(out); err != nil || n > pd.ProfileManagementResponseBudget {
		if err != nil {
			return nil, toTransportError(pd.ErrProfileManagementStateInconsistent)
		}
		return nil, profileManagementTransportError(ctx, pd.ErrProfileManagementResponseTooLarge)
	}
	return out, nil
}
func managedProfileNullable(v *string) *structpb.Value {
	if v == nil {
		return structpb.NewNullValue()
	}
	return structpb.NewStringValue(*v)
}
func managedProfileToken(v string) *structpb.Value {
	if v == "" {
		return structpb.NewNullValue()
	}
	return structpb.NewStringValue(v)
}
func profileManagementStateResponse(s pd.ManagedProfileState) *pb.OwnedApplicationProfileState {
	return &pb.OwnedApplicationProfileState{WorkingProfileRevisionId: managedProfileNullable(s.WorkingProfileRevisionID), CurrentPublishedProfileRevisionId: managedProfileNullable(s.CurrentPublishedProfileRevisionID)}
}
func profileManagementReviewResponse(r *pd.ManagedProfileReview) *pb.OwnedApplicationProfileReview {
	if r == nil {
		return nil
	}
	out := &pb.OwnedApplicationProfileReview{ProfileReviewId: r.ProfileReviewID, Attempt: r.Attempt, Status: r.Status, SourceRevision: r.SourceRevision, SubmittedAt: timestamppb.New(r.SubmittedAt)}
	if d := r.Decision; d != nil {
		out.Decision = &pb.OwnedApplicationProfileReviewDecision{Outcome: d.Outcome, Reason: managedProfileNullable(d.Reason), PolicyVersion: d.PolicyVersion, DecidedAt: timestamppb.New(d.DecidedAt)}
	}
	return out
}
func profileManagementPageResponse(page *pd.ManagedProfilePage) *pb.OwnedApplicationProfileRevisionPage {
	out := &pb.OwnedApplicationProfileRevisionPage{Items: []*pb.OwnedApplicationProfileRevisionSummary{}, ProfileState: profileManagementStateResponse(page.ProfileState), AsOf: timestamppb.New(page.AsOf), NextPageToken: managedProfileToken(page.NextPageToken)}
	for _, p := range page.Items {
		item := &pb.OwnedApplicationProfileRevisionSummary{ProfileRevisionId: p.ProfileRevisionID, Sequence: p.Sequence, ReviewStatus: p.ReviewStatus, Revision: p.Revision, DisplayName: p.DisplayName, Description: managedProfileNullable(p.Description), Icon: managedProfileNullable(p.Icon), CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt), IsCurrentWorkingRevision: p.IsCurrentWorkingRevision, IsCurrentPublished: p.IsCurrentPublished}
		if r := p.LatestReview; r != nil {
			item.LatestReview = &pb.OwnedApplicationProfileLatestReviewSummary{ProfileReviewId: r.ProfileReviewID, Attempt: r.Attempt, Status: r.Status, SubmittedAt: timestamppb.New(r.SubmittedAt)}
			if d := r.Decision; d != nil {
				item.LatestReview.Outcome = &d.Outcome
				item.LatestReview.DecidedAt = timestamppb.New(d.DecidedAt)
			}
		}
		out.Items = append(out.Items, item)
	}
	return out
}
func profileManagementDetailResponse(detail *pd.ManagedProfileDetail) *pb.OwnedApplicationProfileRevisionDetail {
	p := detail.Profile
	out := &pb.OwnedApplicationProfileRevisionDetail{ApplicationId: p.ApplicationID, ProfileRevisionId: p.ProfileRevisionID, Sequence: p.Sequence, ReviewStatus: p.ReviewStatus, Revision: p.Revision, Content: &pb.ApplicationProfileContent{DisplayName: p.DisplayName, Description: managedProfileNullable(p.Description), Icon: managedProfileNullable(p.Icon)}, CreatedBy: p.CreatedBy, CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt), ProfileState: profileManagementStateResponse(detail.ProfileState), IsCurrentWorkingRevision: p.IsCurrentWorkingRevision, IsCurrentPublished: p.IsCurrentPublished, LatestReview: profileManagementReviewResponse(p.LatestReview), Reviews: []*pb.OwnedApplicationProfileReview{}, NextReviewPageToken: managedProfileToken(detail.NextReviewPageToken), AsOf: timestamppb.New(detail.AsOf)}
	for i := range detail.Reviews {
		out.Reviews = append(out.Reviews, profileManagementReviewResponse(&detail.Reviews[i]))
	}
	return out
}

type ProfileManagementResponseSizer struct{}

func NewProfileManagementResponseSizer() *ProfileManagementResponseSizer {
	return &ProfileManagementResponseSizer{}
}

var _ pp.ManagementResponseSizer = (*ProfileManagementResponseSizer)(nil)

func (*ProfileManagementResponseSizer) ListSize(page *pd.ManagedProfilePage) (int, error) {
	return profileManagementWireSize(profileManagementPageResponse(page))
}
func (*ProfileManagementResponseSizer) DetailSize(detail *pd.ManagedProfileDetail) (int, error) {
	return profileManagementWireSize(profileManagementDetailResponse(detail))
}
func profileManagementWireSize(message proto.Message) (int, error) {
	raw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		return 0, err
	}
	return max(len(raw), proto.Size(message)), nil
}
