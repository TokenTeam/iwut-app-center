package transport

import (
	"context"
	"errors"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
	"log/slog"
)

type SubmitApplicationProfileRevisionReviewHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, profileusecase.SubmitApplicationProfileRevisionReviewCommand) (*profiledomain.ApplicationProfileSubmission, error)
}
type ApplicationProfileReviewService struct {
	reviewv1.UnimplementedApplicationProfileReviewServer
	handler SubmitApplicationProfileRevisionReviewHandler
}

func NewApplicationProfileReviewService(h SubmitApplicationProfileRevisionReviewHandler) *ApplicationProfileReviewService {
	return &ApplicationProfileReviewService{handler: h}
}
func (s *ApplicationProfileReviewService) SubmitApplicationProfileRevisionReview(ctx context.Context, request *reviewv1.SubmitApplicationProfileRevisionReviewRequest) (*reviewv1.SubmitApplicationProfileRevisionReviewResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(profiledomain.ErrDeveloperIdentityRequired)
	}
	if request == nil || request.Command == nil || len(request.ProtoReflect().GetUnknown()) != 0 || len(request.Command.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidProfileSubmission()
	}
	if s == nil || s.handler == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	result, err := s.handler.Handle(ctx, identity, profileusecase.SubmitApplicationProfileRevisionReviewCommand{ApplicationID: request.ApplicationId, ProfileRevisionID: request.ProfileRevisionId, ExpectedRevision: request.Command.ExpectedRevision})
	if err != nil {
		if errors.Is(err, profiledomain.ErrApplicationProfileStateInconsistent) {
			slog.ErrorContext(ctx, "application profile state invariant failed", "reason", ReasonApplicationProfileStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.ProfileRevision == nil || result.Review == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	r := result.Review
	snapshot := r.Snapshot()
	description, icon := structpb.NewNullValue(), structpb.NewNullValue()
	if v := snapshot.Description(); v != nil {
		description = structpb.NewStringValue(v.String())
	}
	if v := snapshot.Icon(); v != nil {
		icon = structpb.NewStringValue(v.String())
	}
	return &reviewv1.SubmitApplicationProfileRevisionReviewResponse{ProfileRevision: applicationProfileRevisionResponse(result.ProfileRevision), Review: &reviewv1.ApplicationProfileReviewRecord{
		ProfileReviewId: r.ProfileReviewID().String(), ApplicationId: r.ApplicationID().String(), ProfileRevisionId: r.ProfileRevisionID().String(), Attempt: r.Attempt(), SourceRevision: r.SourceRevision(), Status: string(r.Status()), Snapshot: &profilev1.ApplicationProfileContent{DisplayName: snapshot.DisplayName().String(), Description: description, Icon: icon}, SubmittedBy: r.SubmittedBy().String(), SubmittedAt: timestamppb.New(r.SubmittedAt()), Decision: structpb.NewNullValue(),
	}}, nil
}
func invalidProfileSubmission() error {
	return toTransportError(profiledomain.ErrInvalidApplicationProfileReviewSubmission)
}
