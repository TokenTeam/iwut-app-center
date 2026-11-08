package transport

import (
	"context"
	"errors"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_platform_query"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

const platformApplicationSearchPath = "/v1/platform/applications:search"
const platformApplicationDetailPrefix = "/v1/platform/applications/"

func platformApplicationQueryOperation(op string) bool {
	return op == pb.OperationApplicationPlatformQueryServiceSearchPlatformApplications || op == pb.OperationApplicationPlatformQueryServiceGetPlatformApplicationSummary
}

type PlatformApplicationQueryHandler interface {
	Search(context.Context, shared.TrustedIdentity, domain.PlatformApplicationQuery) (domain.PlatformApplicationPage, error)
	Get(context.Context, shared.TrustedIdentity, string) (domain.PlatformApplicationSummary, error)
}
type ApplicationPlatformQueryService struct {
	pb.UnimplementedApplicationPlatformQueryServiceServer
	handler PlatformApplicationQueryHandler
}

func NewApplicationPlatformQueryService(handler PlatformApplicationQueryHandler) *ApplicationPlatformQueryService {
	return &ApplicationPlatformQueryService{handler: handler}
}
func platformQueryIdentity(ctx context.Context) (shared.TrustedIdentity, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	return identity, usecase.AuthorizePlatformApplicationQuery(identity)
}
func (s *ApplicationPlatformQueryService) SearchPlatformApplications(ctx context.Context, req *pb.SearchPlatformApplicationsRequest) (*pb.PlatformApplicationPage, error) {
	setPrivateNoStore(ctx)
	identity, err := platformQueryIdentity(ctx)
	if err != nil {
		return nil, platformApplicationError(err)
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 || (req.Query != nil && len(req.Query.ProtoReflect().GetUnknown()) != 0) {
		return nil, platformApplicationError(domain.ErrInvalidPlatformApplicationQuery)
	}
	q := req.GetQuery()
	query, err := domain.NewPlatformApplicationQuery(domain.PlatformApplicationQuery{ApplicationID: q.GetApplicationId(), AdminAuthID: q.GetAdminAuthId(), NamePrefix: q.GetNamePrefix(), LifecycleStatuses: q.GetLifecycleStatuses(), PlatformAvailabilityStatuses: q.GetPlatformAvailabilityStatuses(), ArchiveStatuses: q.GetArchiveStatuses(), PageSize: q.GetPageSize(), PageToken: q.GetPageToken()})
	if err != nil {
		return nil, platformApplicationError(err)
	}
	if s == nil || s.handler == nil {
		return nil, platformApplicationError(domain.ErrPlatformApplicationUnavailable)
	}
	page, err := s.handler.Search(ctx, identity, query)
	if err != nil {
		return nil, platformApplicationError(err)
	}
	if page.Validate(query, page.AsOf) != nil {
		return nil, platformApplicationError(domain.ErrPlatformApplicationStateInconsistent)
	}
	result := &pb.PlatformApplicationPage{Items: make([]*pb.PlatformApplicationSummary, 0, len(page.Items)), NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}
	for _, item := range page.Items {
		result.Items = append(result.Items, platformApplicationResource(item))
	}
	return result, nil
}
func (s *ApplicationPlatformQueryService) GetPlatformApplicationSummary(ctx context.Context, req *pb.GetPlatformApplicationSummaryRequest) (*pb.PlatformApplicationSummary, error) {
	setPrivateNoStore(ctx)
	identity, err := platformQueryIdentity(ctx)
	if err != nil {
		return nil, platformApplicationError(err)
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return nil, platformApplicationError(domain.ErrInvalidPlatformApplicationQuery)
	}
	if _, ok := shared.ParseApplicationID(req.GetApplicationId()); !ok {
		return nil, platformApplicationError(domain.ErrInvalidPlatformApplicationQuery)
	}
	if s == nil || s.handler == nil {
		return nil, platformApplicationError(domain.ErrPlatformApplicationUnavailable)
	}
	item, err := s.handler.Get(ctx, identity, req.GetApplicationId())
	if err != nil {
		return nil, platformApplicationError(err)
	}
	if item.Validate() != nil || item.ApplicationID.String() != req.ApplicationId {
		return nil, platformApplicationError(domain.ErrPlatformApplicationStateInconsistent)
	}
	return platformApplicationResource(item), nil
}
func platformApplicationResource(s domain.PlatformApplicationSummary) *pb.PlatformApplicationSummary {
	return &pb.PlatformApplicationSummary{ApplicationId: s.ApplicationID.String(), Name: s.Name, AdminAuthId: s.AdminAuthID.String(), CreatedAt: timestamppb.New(s.CreatedAt), OwnershipRevision: s.OwnershipRevision, LifecycleStatus: string(s.LifecycleStatus), LifecycleRevision: s.LifecycleRevision, PlatformAvailabilityStatus: string(s.PlatformAvailabilityStatus), PlatformAvailabilityRevision: s.PlatformAvailabilityRevision, ArchiveStatus: string(s.ArchiveStatus), ArchiveRevision: s.ArchiveRevision}
}
func platformApplicationError(err error) error {
	code, reason, message := codes.Internal, "ERROR_REASON_PLATFORM_APPLICATION_STATE_INCONSISTENT", "platform application state is inconsistent"
	switch {
	case errors.Is(err, domain.ErrUserIdentityRequired):
		code, reason, message = codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required"
	case errors.Is(err, domain.ErrApplicationPlatformSearchRequired):
		code, reason, message = codes.PermissionDenied, "ERROR_REASON_APPLICATION_PLATFORM_SEARCH_REQUIRED", "application platform search permission is required"
	case errors.Is(err, domain.ErrInvalidPlatformApplicationQuery):
		code, reason, message = codes.InvalidArgument, "ERROR_REASON_INVALID_PLATFORM_APPLICATION_QUERY", "platform application query is invalid"
	case errors.Is(err, domain.ErrInvalidPlatformApplicationPageToken):
		code, reason, message = codes.InvalidArgument, "ERROR_REASON_INVALID_PLATFORM_APPLICATION_PAGE_TOKEN", "platform application page token is invalid"
	case errors.Is(err, domain.ErrPlatformApplicationNotFound):
		code, reason, message = codes.NotFound, "ERROR_REASON_PLATFORM_APPLICATION_NOT_FOUND", "platform application was not found"
	case errors.Is(err, domain.ErrPlatformApplicationUnavailable):
		code, reason, message = codes.Unavailable, "ERROR_REASON_PLATFORM_APPLICATION_UNAVAILABLE", "platform application query is unavailable"
	}
	return transportStatus(code, reason, message)
}
