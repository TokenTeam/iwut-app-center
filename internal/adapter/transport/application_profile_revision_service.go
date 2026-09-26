package transport

import (
	"context"
	"errors"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"log/slog"
	"net/http"
	"strconv"

	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type CreateApplicationProfileRevisionHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, profileusecase.CreateApplicationProfileRevisionCommand) (*profiledomain.ApplicationProfileRevision, error)
}

type UpdateDraftApplicationProfileRevisionHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, profileusecase.UpdateDraftApplicationProfileRevisionCommand) (*profiledomain.ApplicationProfileRevision, error)
}

type ApplicationProfileRevisionService struct {
	profilev1.UnimplementedApplicationProfileRevisionServer
	createHandler CreateApplicationProfileRevisionHandler
	updateHandler UpdateDraftApplicationProfileRevisionHandler
}

var _ profilev1.ApplicationProfileRevisionServer = (*ApplicationProfileRevisionService)(nil)
var _ profilev1.ApplicationProfileRevisionHTTPServer = (*ApplicationProfileRevisionService)(nil)

func NewApplicationProfileRevisionService(handler CreateApplicationProfileRevisionHandler, update UpdateDraftApplicationProfileRevisionHandler) *ApplicationProfileRevisionService {
	return &ApplicationProfileRevisionService{createHandler: handler, updateHandler: update}
}

func (s *ApplicationProfileRevisionService) CreateApplicationProfileRevision(ctx context.Context, request *profilev1.CreateApplicationProfileRevisionRequest) (*profilev1.CreateApplicationProfileRevisionResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(profiledomain.ErrDeveloperIdentityRequired)
	}
	if request == nil {
		return nil, invalidCreateApplicationProfileRevisionRequest()
	}
	if s == nil || s.createHandler == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	if len(request.ProtoReflect().GetUnknown()) != 0 || request.GetProfile() == nil || len(request.GetProfile().ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidCreateApplicationProfileRevisionRequest()
	}
	description, ok := profileNullableString(request.GetProfile().GetDescription())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationDescription)
	}
	icon, ok := profileNullableString(request.GetProfile().GetIcon())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationIcon)
	}
	revision, err := s.createHandler.Handle(ctx, identity, profileusecase.CreateApplicationProfileRevisionCommand{
		ApplicationID: request.GetApplicationId(), DisplayName: request.GetProfile().GetDisplayName(), Description: description, Icon: icon,
	})
	if err != nil {
		if errors.Is(err, profiledomain.ErrApplicationProfileStateInconsistent) {
			slog.ErrorContext(ctx, "application profile state invariant failed", "reason", ReasonApplicationProfileStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if revision == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	return applicationProfileRevisionResponse(revision), nil
}

// Value preserves the distinction between absent and explicitly null for gRPC.
func profileNullableString(value *structpb.Value) (*string, bool) {
	if value == nil || len(value.ProtoReflect().GetUnknown()) != 0 {
		return nil, false
	}
	switch kind := value.Kind.(type) {
	case *structpb.Value_StringValue:
		if kind == nil {
			return nil, false
		}
		result := kind.StringValue
		return &result, true
	case *structpb.Value_NullValue:
		return nil, kind != nil && kind.NullValue == structpb.NullValue_NULL_VALUE
	default:
		return nil, false
	}
}

func applicationProfileRevisionResponse(revision *profiledomain.ApplicationProfileRevision) *profilev1.CreateApplicationProfileRevisionResponse {
	description, icon := structpb.NewNullValue(), structpb.NewNullValue()
	if value := revision.Description(); value != nil {
		description = structpb.NewStringValue(value.String())
	}
	if value := revision.Icon(); value != nil {
		icon = structpb.NewStringValue(value.String())
	}
	return &profilev1.CreateApplicationProfileRevisionResponse{
		ProfileRevisionId: revision.ProfileRevisionID().String(), ApplicationId: revision.ApplicationID().String(), Sequence: int32(revision.Sequence()),
		DisplayName: revision.DisplayName().String(), Description: description, Icon: icon, ReviewStatus: string(revision.ReviewStatus()),
		CreatedBy: revision.CreatedBy().String(), CreatedAt: timestamppb.New(revision.CreatedAt()), Revision: revision.Revision(), UpdatedBy: revision.UpdatedBy().String(), UpdatedAt: timestamppb.New(revision.UpdatedAt()),
	}
}

func invalidCreateApplicationProfileRevisionRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidCreateApplicationProfileRevisionRequest, "profile draft requires only displayName, description and icon")
}

func (s *ApplicationProfileRevisionService) UpdateApplicationProfileRevision(ctx context.Context, request *profilev1.UpdateApplicationProfileRevisionRequest) (*profilev1.UpdateApplicationProfileRevisionResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(profiledomain.ErrDeveloperIdentityRequired)
	}
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || request.GetProfile() == nil || len(request.GetProfile().ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidUpdateApplicationProfileRevisionRequest()
	}
	if s == nil || s.updateHandler == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	expected, err := profileExpectedRevision(ctx, request.GetExpectedRevision())
	if err != nil {
		return nil, err
	}
	description, ok := profileNullableString(request.GetProfile().GetDescription())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationDescription)
	}
	icon, ok := profileNullableString(request.GetProfile().GetIcon())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationIcon)
	}
	revision, err := s.updateHandler.Handle(ctx, identity, profileusecase.UpdateDraftApplicationProfileRevisionCommand{
		ApplicationID: request.GetApplicationId(), ProfileRevisionID: request.GetProfileRevisionId(), ExpectedRevision: expected,
		DisplayName: request.GetProfile().GetDisplayName(), Description: description, Icon: icon,
	})
	if err != nil {
		if errors.Is(err, profiledomain.ErrApplicationProfileStateInconsistent) {
			slog.ErrorContext(ctx, "application profile state invariant failed", "reason", ReasonApplicationProfileStateInconsistent)
		}
		if isHTTP(ctx) && errors.Is(err, profiledomain.ErrApplicationProfileRevisionConflict) {
			return nil, kratoserrors.New(http.StatusPreconditionFailed, ReasonApplicationProfileRevisionConflict, "application profile revision conflicts")
		}
		return nil, toTransportError(err)
	}
	if revision == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	response := applicationProfileRevisionResponse(revision)
	return &profilev1.UpdateApplicationProfileRevisionResponse{
		ProfileRevisionId: response.ProfileRevisionId, ApplicationId: response.ApplicationId, Sequence: response.Sequence,
		DisplayName: response.DisplayName, Description: response.Description, Icon: response.Icon, ReviewStatus: response.ReviewStatus,
		CreatedBy: response.CreatedBy, CreatedAt: response.CreatedAt, Revision: response.Revision, UpdatedBy: response.UpdatedBy, UpdatedAt: response.UpdatedAt,
	}, nil
}

func profileExpectedRevision(ctx context.Context, grpcRevision int64) (int64, error) {
	transporter, ok := kratostransport.FromServerContext(ctx)
	if !ok || transporter.Kind() != kratostransport.KindHTTP {
		if grpcRevision < 1 {
			return 0, toTransportError(profiledomain.ErrApplicationProfileExpectedRevisionRequired)
		}
		return grpcRevision, nil
	}
	return parseProfileIfMatch(transporter.RequestHeader().Values("If-Match"))
}

func parseProfileIfMatch(values []string) (int64, error) {
	if len(values) == 0 {
		return 0, kratoserrors.New(http.StatusPreconditionRequired, ReasonApplicationProfileExpectedRevisionRequired, "If-Match is required")
	}
	invalid := func() (int64, error) {
		return 0, toTransportError(profiledomain.ErrApplicationProfileExpectedRevisionRequired)
	}
	if len(values) != 1 {
		return invalid()
	}
	value := values[0]
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return invalid()
	}
	for _, character := range value[1 : len(value)-1] {
		if character < '0' || character > '9' {
			return invalid()
		}
	}
	revision, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || revision < 1 {
		return invalid()
	}
	return revision, nil
}

func invalidUpdateApplicationProfileRevisionRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidUpdateApplicationProfileRevisionRequest, "profile draft replacement requires only displayName, description and icon")
}
