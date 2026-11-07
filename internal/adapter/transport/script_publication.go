package transport

import (
	"context"
	"errors"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_publication"
	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/script/domain"
	scriptuc "iwut-app-center/internal/script/usecase"
)

const (
	publishScriptVersion      = publicationv1.OperationScriptPublicationServicePublishScriptVersion
	clearScriptHead           = publicationv1.OperationScriptPublicationServiceClearScriptHead
	suspendScriptDistribution = publicationv1.OperationScriptPublicationServiceSuspendScriptDistribution
	restoreScriptDistribution = publicationv1.OperationScriptPublicationServiceRestoreScriptDistribution
)

func scriptPublicationAction(operation string) string {
	switch operation {
	case publishScriptVersion:
		return domain.PublishScript
	case clearScriptHead:
		return domain.ClearScriptHead
	case suspendScriptDistribution:
		return domain.SuspendScript
	case restoreScriptDistribution:
		return domain.RestoreScript
	default:
		return ""
	}
}

func (s *ScriptManagementService) PublishScriptVersion(ctx context.Context, req *publicationv1.PublishScriptVersionRequest) (*publicationv1.ScriptPublicationCommandResult, error) {
	command := req.GetCommand()
	return s.applyScriptPublication(ctx, req, command, scriptuc.PublicationCommand{Action: domain.PublishScript,
		ResourceID: req.GetResourceId(), VersionID: req.GetVersionId(), ExpectedRevision: command.GetExpectedPublicationRevision()})
}

func (s *ScriptManagementService) ClearScriptHead(ctx context.Context, req *publicationv1.ClearScriptHeadRequest) (*publicationv1.ScriptPublicationCommandResult, error) {
	command := req.GetCommand()
	return s.applyScriptPublication(ctx, req, command, scriptuc.PublicationCommand{Action: domain.ClearScriptHead,
		ResourceID: req.GetResourceId(), ExpectedRevision: command.GetExpectedPublicationRevision()})
}

func (s *ScriptManagementService) SuspendScriptDistribution(ctx context.Context, req *publicationv1.SuspendScriptDistributionRequest) (*publicationv1.ScriptPublicationCommandResult, error) {
	command := req.GetCommand()
	return s.applyScriptPublication(ctx, req, command, scriptuc.PublicationCommand{Action: domain.SuspendScript,
		ResourceID: req.GetResourceId(), ExpectedRevision: command.GetExpectedAvailabilityRevision(), Reason: command.GetReason()})
}

func (s *ScriptManagementService) RestoreScriptDistribution(ctx context.Context, req *publicationv1.RestoreScriptDistributionRequest) (*publicationv1.ScriptPublicationCommandResult, error) {
	command := req.GetCommand()
	return s.applyScriptPublication(ctx, req, command, scriptuc.PublicationCommand{Action: domain.RestoreScript,
		ResourceID: req.GetResourceId(), ExpectedRevision: command.GetExpectedAvailabilityRevision(), Reason: command.GetReason()})
}

func (s *ScriptManagementService) applyScriptPublication(ctx context.Context, request, input proto.Message, command scriptuc.PublicationCommand) (*publicationv1.ScriptPublicationCommandResult, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	if err := scriptuc.RequirePublicationPermission(identity, command.Action); err != nil {
		return nil, scriptPublicationError(err)
	}
	if !request.ProtoReflect().IsValid() || !input.ProtoReflect().IsValid() || hasUnknownScriptFields(request) || hasUnknownScriptFields(input) {
		return nil, scriptPublicationError(domain.ErrInvalidPublicationRequest)
	}
	result, err := s.publications.Apply(ctx, identity, command)
	if err != nil {
		return nil, scriptPublicationError(err)
	}
	out := &publicationv1.ScriptPublicationCommandResult{State: scriptPublicationStateResponse(result.State), Changed: result.Event != nil}
	if result.Event != nil {
		out.EventId = string(result.Event.ID)
		out.EventAt = timestamppb.New(result.Event.At)
	}
	return out, nil
}

func scriptPublishedVersionResponse(v *domain.PublishedScript) *publicationv1.ScriptPublishedVersion {
	if v == nil {
		return nil
	}
	return &publicationv1.ScriptPublishedVersion{VersionId: string(v.VersionID), ReviewId: string(v.ReviewID), DecisionId: v.DecisionID,
		VersionLabel: v.Label, ContentFormat: v.Format, Digest: v.Digest, ByteLength: v.ByteLength}
}

func scriptPublicationStateResponse(s domain.PublicationState) *publicationv1.ScriptPublicationState {
	return &publicationv1.ScriptPublicationState{ResourceId: string(s.ResourceID), Head: scriptPublishedVersionResponse(s.Head),
		PublicationRevision: s.PublicationRevision, AvailabilityRevision: s.AvailabilityRevision,
		Availability:         publicationv1.ScriptDistributionAvailability(publicationv1.ScriptDistributionAvailability_value["SCRIPT_DISTRIBUTION_AVAILABILITY_"+s.Availability]),
		PublicationUpdatedAt: timestamppb.New(s.PublicationUpdatedAt), AvailabilityUpdatedAt: timestamppb.New(s.AvailabilityUpdatedAt)}
}

func (s *ScriptQueryService) GetScriptPublication(ctx context.Context, req *queryv1.GetScriptPublicationRequest) (*publicationv1.ScriptPublicationState, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptPublicationRead, req)
	if err != nil {
		return nil, err
	}
	state, err := s.query.GetPublication(ctx, identity, req.GetResourceId())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	return scriptPublicationStateResponse(state), nil
}

func (s *ScriptQueryService) ListScriptPublicationHistory(ctx context.Context, req *queryv1.ListScriptPublicationHistoryRequest) (*queryv1.ListScriptPublicationHistoryResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptPublicationHistoryRead, req)
	if err != nil {
		return nil, err
	}
	page, err := s.query.ListPublicationHistory(ctx, identity, req.GetResourceId(), req.Limit, req.GetCursor())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	out := &queryv1.ListScriptPublicationHistoryResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, &publicationv1.ScriptPublicationHistoryItem{EventId: string(item.EventID), ResourceId: string(item.ResourceID),
			Action:              publicationv1.ScriptPublicationAction(publicationv1.ScriptPublicationAction_value["SCRIPT_PUBLICATION_ACTION_"+item.Action]),
			PublicationRevision: item.Revision, Version: scriptPublishedVersionResponse(item.Version), At: timestamppb.New(item.At)})
	}
	return out, nil
}

func scriptPublicationError(err error) error {
	for _, mapping := range []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{domain.ErrIdentityRequired, codes.Unauthenticated, "USER_IDENTITY_REQUIRED"},
		{domain.ErrPublicationForbidden, codes.PermissionDenied, "SCRIPT_PUBLICATION_FORBIDDEN"},
		{domain.ErrInvalidPublicationRequest, codes.InvalidArgument, "INVALID_SCRIPT_PUBLICATION_REQUEST"},
		{domain.ErrInvalidResourceID, codes.InvalidArgument, "INVALID_SCRIPT_RESOURCE_ID"},
		{domain.ErrInvalidVersionID, codes.InvalidArgument, "INVALID_SCRIPT_VERSION_ID"},
		{domain.ErrNotFound, codes.NotFound, "SCRIPT_RESOURCE_NOT_FOUND"},
		{domain.ErrVersionNotFound, codes.NotFound, "SCRIPT_VERSION_NOT_FOUND"},
		{domain.ErrPublicationRevisionConflict, codes.Aborted, "SCRIPT_PUBLICATION_REVISION_CONFLICT"},
		{domain.ErrAvailabilityRevisionConflict, codes.Aborted, "SCRIPT_AVAILABILITY_REVISION_CONFLICT"},
		{domain.ErrScriptNotApproved, codes.FailedPrecondition, "SCRIPT_VERSION_NOT_APPROVED"},
		{domain.ErrDistributionSuspended, codes.FailedPrecondition, "SCRIPT_DISTRIBUTION_SUSPENDED"},
		{domain.ErrPublicationInconsistent, codes.Internal, "SCRIPT_PUBLICATION_DATA_INCONSISTENT"},
	} {
		if errors.Is(err, mapping.err) {
			return transportStatus(mapping.code, "ERROR_REASON_"+mapping.reason, mapping.err.Error())
		}
	}
	return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "script publication operation failed")
}
