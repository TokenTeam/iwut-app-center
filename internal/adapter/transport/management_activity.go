package transport

import (
	"context"
	"errors"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management_activity"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"net/http"
)

func managementActivityOperation(op string) bool {
	return op == pb.OperationApplicationManagementActivityServiceListApplicationManagementActivity || op == pb.OperationApplicationManagementActivityServiceGetApplicationManagementActivity
}

type ManagementActivityQueryHandler interface {
	List(context.Context, shared.TrustedIdentity, string, ad.ManagementActivityQuery) (ad.ManagementActivityPage, error)
	Get(context.Context, shared.TrustedIdentity, string, string) (ad.ManagementActivityDetail, error)
}
type ApplicationManagementActivityService struct {
	pb.UnimplementedApplicationManagementActivityServiceServer
	handler ManagementActivityQueryHandler
}

func NewApplicationManagementActivityService(h ManagementActivityQueryHandler) *ApplicationManagementActivityService {
	return &ApplicationManagementActivityService{handler: h}
}
func (s *ApplicationManagementActivityService) ListApplicationManagementActivity(ctx context.Context, req *pb.ListApplicationManagementActivityRequest) (*pb.ApplicationManagementActivityPage, error) {
	setPrivateNoStore(ctx)
	identity, _ := trustedIdentityFromContext(ctx)
	if !identity.AuthID.IsValid() {
		return nil, activityTransportError(ctx, ad.ErrUserIdentityRequired)
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 || req.Query != nil && len(req.Query.ProtoReflect().GetUnknown()) != 0 {
		return nil, activityTransportError(ctx, ad.ErrInvalidManagementActivityQuery)
	}
	app, valid := shared.ParseApplicationID(req.GetApplicationId())
	q := ad.ManagementActivityQuery{PageSize: req.GetQuery().GetPageSize(), PageToken: req.GetQuery().GetPageToken()}
	for _, k := range req.GetQuery().GetKinds() {
		name, ok := pb.ApplicationManagementActivityKind_name[int32(k)]
		if !ok {
			return nil, activityTransportError(ctx, ad.ErrInvalidManagementActivityQuery)
		}
		q.Kinds = append(q.Kinds, ad.ManagementActivityKind(name))
	}
	q, err := ad.NewManagementActivityQuery(q)
	if !valid || err != nil {
		return nil, activityTransportError(ctx, ad.ErrInvalidManagementActivityQuery)
	}
	if s == nil || s.handler == nil {
		return nil, activityTransportError(ctx, ad.ErrManagementActivityUnavailable)
	}
	page, err := s.handler.List(ctx, identity, req.ApplicationId, q)
	if err != nil {
		return nil, activityTransportError(ctx, err)
	}
	if page.Validate(app, q, page.AsOf) != nil {
		return nil, activityTransportError(ctx, ad.ErrManagementActivityStateInconsistent)
	}
	out := &pb.ApplicationManagementActivityPage{Items: make([]*pb.ApplicationManagementActivity, 0, len(page.Items)), NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}
	for _, a := range page.Items {
		out.Items = append(out.Items, activityResource(a))
	}
	if err := activityResponseBudget(out); err != nil {
		return nil, activityTransportError(ctx, err)
	}
	return out, nil
}
func (s *ApplicationManagementActivityService) GetApplicationManagementActivity(ctx context.Context, req *pb.GetApplicationManagementActivityRequest) (*pb.ApplicationManagementActivityDetail, error) {
	setPrivateNoStore(ctx)
	identity, _ := trustedIdentityFromContext(ctx)
	if !identity.AuthID.IsValid() {
		return nil, activityTransportError(ctx, ad.ErrUserIdentityRequired)
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return nil, activityTransportError(ctx, ad.ErrInvalidManagementActivityQuery)
	}
	app, ok := shared.ParseApplicationID(req.GetApplicationId())
	id, err := ad.ParseManagementActivityID(req.GetActivityId())
	if !ok || err != nil {
		return nil, activityTransportError(ctx, ad.ErrInvalidManagementActivityQuery)
	}
	if s == nil || s.handler == nil {
		return nil, activityTransportError(ctx, ad.ErrManagementActivityUnavailable)
	}
	detail, err := s.handler.Get(ctx, identity, req.ApplicationId, req.ActivityId)
	if err != nil {
		return nil, activityTransportError(ctx, err)
	}
	if detail.Validate(app, id, detail.AsOf) != nil {
		return nil, activityTransportError(ctx, ad.ErrManagementActivityStateInconsistent)
	}
	reason := structpb.NewNullValue()
	if detail.RejectionReason != nil {
		reason = structpb.NewStringValue(*detail.RejectionReason)
	}
	out := &pb.ApplicationManagementActivityDetail{Activity: activityResource(detail.Activity), RejectionReason: reason, AsOf: timestamppb.New(detail.AsOf)}
	if err := activityResponseBudget(out); err != nil {
		return nil, activityTransportError(ctx, err)
	}
	return out, nil
}
func activityResource(a ad.ManagementActivity) *pb.ApplicationManagementActivity {
	out := &pb.ApplicationManagementActivity{ActivityId: a.ActivityID, ApplicationId: a.ApplicationID.String(), Kind: pb.ApplicationManagementActivityKind(pb.ApplicationManagementActivityKind_value[string(a.Kind)]), OccurredAt: timestamppb.New(a.OccurredAt)}
	if v := a.VersionReview; v != nil {
		out.Target = &pb.ApplicationManagementActivity_VersionReview{VersionReview: &pb.VersionReviewRef{VersionId: v.VersionID, ReviewId: v.ReviewID, Attempt: v.Attempt, VersionLabelAtSubmission: *a.VersionLabelAtSubmission}}
	} else {
		p := a.ProfileReview
		out.Target = &pb.ApplicationManagementActivity_ProfileReview{ProfileReview: &pb.ProfileReviewRef{ProfileRevisionId: p.ProfileRevisionID, ProfileReviewId: p.ProfileReviewID, Attempt: p.Attempt, ProfileRevisionSequence: int32(a.ProfileRevisionSequence)}}
	}
	return out
}
func activityResponseBudget(out proto.Message) error {
	raw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(out)
	if err != nil {
		return ad.ErrManagementActivityStateInconsistent
	}
	if len(raw) > ad.ManagementActivityResponseBudget || proto.Size(out) > ad.ManagementActivityResponseBudget {
		return ad.ErrManagementActivityResponseTooLarge
	}
	return nil
}
func activityTransportError(ctx context.Context, err error) error {
	code, reason, message := codes.Internal, "ERROR_REASON_APPLICATION_MANAGEMENT_ACTIVITY_STATE_INCONSISTENT", "application management activity state is inconsistent"
	switch {
	case errors.Is(err, ad.ErrUserIdentityRequired):
		code, reason, message = codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required"
	case errors.Is(err, ad.ErrInvalidManagementActivityQuery):
		code, reason, message = codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_MANAGEMENT_ACTIVITY_QUERY", "application management activity query is invalid"
	case errors.Is(err, ad.ErrInvalidManagementActivityPageToken):
		code, reason, message = codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_MANAGEMENT_ACTIVITY_PAGE_TOKEN", "application management activity page token is invalid"
	case errors.Is(err, ad.ErrManagementActivityNotFound):
		code, reason, message = codes.NotFound, "ERROR_REASON_APPLICATION_MANAGEMENT_ACTIVITY_NOT_FOUND", "application management activity was not found"
	case errors.Is(err, ad.ErrManagementActivityUnavailable):
		code, reason, message = codes.Unavailable, "ERROR_REASON_APPLICATION_MANAGEMENT_ACTIVITY_UNAVAILABLE", "application management activity is unavailable"
	case errors.Is(err, ad.ErrManagementActivityResponseTooLarge):
		reason, message = "ERROR_REASON_APPLICATION_MANAGEMENT_ACTIVITY_RESPONSE_TOO_LARGE", "application management activity response is too large"
		if isHTTP(ctx) {
			return kerrors.New(http.StatusRequestEntityTooLarge, reason, message)
		}
		code = codes.ResourceExhausted
	}
	return transportStatus(code, reason, message)
}
