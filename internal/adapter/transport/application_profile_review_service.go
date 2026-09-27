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
	"time"
)

type SubmitApplicationProfileRevisionReviewHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, profileusecase.SubmitApplicationProfileRevisionReviewCommand) (*profiledomain.ApplicationProfileSubmission, error)
}
type DecideApplicationProfileRevisionReviewHandler interface {
	Handle(context.Context, profileusecase.ReviewerIdentity, profileusecase.DecideApplicationProfileRevisionReviewCommand) (*profiledomain.ApplicationProfileDecisionResult, error)
}
type ApplicationProfileReviewService struct {
	reviewv1.UnimplementedApplicationProfileReviewServer
	handler       SubmitApplicationProfileRevisionReviewHandler
	decideHandler DecideApplicationProfileRevisionReviewHandler
}

func NewApplicationProfileReviewService(h SubmitApplicationProfileRevisionReviewHandler, d DecideApplicationProfileRevisionReviewHandler) *ApplicationProfileReviewService {
	return &ApplicationProfileReviewService{handler: h, decideHandler: d}
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
	return &reviewv1.SubmitApplicationProfileRevisionReviewResponse{ProfileRevision: applicationProfileRevisionResponse(result.ProfileRevision), Review: applicationProfileReviewResponse(result.Review)}, nil
}
func applicationProfileReviewResponse(r *profiledomain.ApplicationProfileReview) *reviewv1.ApplicationProfileReviewRecord {
	snapshot := r.Snapshot()
	description, icon := structpb.NewNullValue(), structpb.NewNullValue()
	if v := snapshot.Description(); v != nil {
		description = structpb.NewStringValue(v.String())
	}
	if v := snapshot.Icon(); v != nil {
		icon = structpb.NewStringValue(v.String())
	}
	decision := structpb.NewNullValue()
	if d := r.Decision(); d != nil {
		checks := make([]*structpb.Value, len(d.ConfirmedCheckIDs))
		for i, id := range d.ConfirmedCheckIDs {
			checks[i] = structpb.NewStringValue(id)
		}
		reason := structpb.NewNullValue()
		if d.Reason != nil {
			reason = structpb.NewStringValue(*d.Reason)
		}
		decision = structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
			"outcome": structpb.NewStringValue(string(d.Outcome)), "policyVersion": structpb.NewStringValue(d.PolicyVersion),
			"confirmedCheckIds": structpb.NewListValue(&structpb.ListValue{Values: checks}), "reason": reason,
			"decidedBy": structpb.NewStringValue(d.DecidedBy.String()), "decidedAt": structpb.NewStringValue(d.DecidedAt.UTC().Format(time.RFC3339Nano)),
		}})
	}
	return &reviewv1.ApplicationProfileReviewRecord{
		ProfileReviewId: r.ProfileReviewID().String(), ApplicationId: r.ApplicationID().String(), ProfileRevisionId: r.ProfileRevisionID().String(), Attempt: r.Attempt(), SourceRevision: r.SourceRevision(), Status: string(r.Status()), Snapshot: &profilev1.ApplicationProfileContent{DisplayName: snapshot.DisplayName().String(), Description: description, Icon: icon}, SubmittedBy: r.SubmittedBy().String(), SubmittedAt: timestamppb.New(r.SubmittedAt()), Decision: decision,
	}
}
func (s *ApplicationProfileReviewService) DecideApplicationProfileRevisionReview(ctx context.Context, request *reviewv1.DecideApplicationProfileRevisionReviewRequest) (*reviewv1.DecideApplicationProfileRevisionReviewResponse, error) {
	trusted, ok := trustedIdentityFromContext(ctx)
	if !ok || !trusted.AuthID.IsValid() {
		return nil, toTransportError(profiledomain.ErrReviewerIdentityRequired)
	}
	if request == nil || request.Command == nil || len(request.ProtoReflect().GetUnknown()) != 0 || len(request.Command.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidProfileDecision()
	}
	c := request.Command
	command := profileusecase.DecideApplicationProfileRevisionReviewCommand{ApplicationID: request.ApplicationId, ProfileRevisionID: request.ProfileRevisionId, ProfileReviewID: request.ProfileReviewId, ExpectedProfileRevisionRevision: c.ExpectedProfileRevisionRevision, ExpectedPolicyVersion: c.ExpectedPolicyVersion, ConfirmedCheckIDs: append([]string{}, c.ConfirmedCheckIds...), Reason: c.Reason}
	switch c.Outcome {
	case reviewv1.ProfileReviewDecisionAction_APPROVE:
		command.Outcome = "APPROVE"
	case reviewv1.ProfileReviewDecisionAction_REJECT:
		command.Outcome = "REJECT"
	default:
		return nil, invalidProfileDecision()
	}
	if v := c.ExpectedCurrentPublishedProfileRevisionId; v != nil {
		if len(v.ProtoReflect().GetUnknown()) != 0 {
			return nil, invalidProfileDecision()
		}
		command.PublicationPreconditionPresent = true
		switch x := v.Kind.(type) {
		case *structpb.Value_NullValue:
			if x.NullValue != structpb.NullValue_NULL_VALUE {
				return nil, invalidProfileDecision()
			}
		case *structpb.Value_StringValue:
			command.ExpectedCurrentPublishedProfileRevisionID = &x.StringValue
		default:
			return nil, invalidProfileDecision()
		}
	}
	if s == nil || s.decideHandler == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	result, err := s.decideHandler.Handle(ctx, profileusecase.ReviewerIdentity{AuthID: trusted.AuthID, Permissions: append([]string{}, trusted.Permissions...)}, command)
	if err != nil {
		if errors.Is(err, profiledomain.ErrApplicationProfileReviewStateInconsistent) {
			slog.ErrorContext(ctx, "application profile review state invariant failed", "reason", ReasonApplicationProfileReviewStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.ProfileRevision == nil || result.Review == nil {
		return nil, toTransportError(profiledomain.NewInternalError(nil))
	}
	published := structpb.NewNullValue()
	if result.CurrentPublishedProfileRevisionID != nil {
		published = structpb.NewStringValue(result.CurrentPublishedProfileRevisionID.String())
	}
	return &reviewv1.DecideApplicationProfileRevisionReviewResponse{ProfileRevision: applicationProfileRevisionResponse(result.ProfileRevision), Review: applicationProfileReviewResponse(result.Review), CurrentPublishedProfileRevisionId: published}, nil
}
func invalidProfileDecision() error {
	return toTransportError(profiledomain.ErrInvalidApplicationProfileReviewDecision)
}

func invalidProfileSubmission() error {
	return toTransportError(profiledomain.ErrInvalidApplicationProfileReviewSubmission)
}
