package transport

import (
	"context"
	"errors"
	"log/slog"

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

type ApplicationProfileRevisionService struct {
	profilev1.UnimplementedApplicationProfileRevisionServer
	createHandler CreateApplicationProfileRevisionHandler
}

var _ profilev1.ApplicationProfileRevisionServer = (*ApplicationProfileRevisionService)(nil)
var _ profilev1.ApplicationProfileRevisionHTTPServer = (*ApplicationProfileRevisionService)(nil)

func NewApplicationProfileRevisionService(handler CreateApplicationProfileRevisionHandler) *ApplicationProfileRevisionService {
	return &ApplicationProfileRevisionService{createHandler: handler}
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
	if len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidCreateApplicationProfileRevisionRequest()
	}
	description, ok := profileNullableString(request.GetDescription())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationDescription)
	}
	icon, ok := profileNullableString(request.GetIcon())
	if !ok {
		return nil, toTransportError(profiledomain.ErrInvalidApplicationIcon)
	}
	revision, err := s.createHandler.Handle(ctx, identity, profileusecase.CreateApplicationProfileRevisionCommand{
		ApplicationID: request.GetApplicationId(), DisplayName: request.GetDisplayName(), Description: description, Icon: icon,
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
