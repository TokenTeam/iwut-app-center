package transport

import (
	"context"
	"errors"

	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_review"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	scriptuc "iwut-app-center/internal/script/usecase"
)

const decideScriptReview = reviewv1.OperationScriptReviewServiceDecideScriptVersionReview

func (s *ScriptManagementService) DecideScriptVersionReview(ctx context.Context, req *reviewv1.DecideScriptVersionReviewRequest) (*reviewv1.ScriptReviewDecisionReceipt, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	if err := scriptuc.RequireScriptReviewer(identity); err != nil {
		return nil, scriptDecisionError(err)
	}
	command := req.GetCommand()
	if req == nil || command == nil || hasUnknownScriptFields(req) || hasUnknownScriptFields(command) {
		return nil, scriptDecisionError(domain.ErrInvalidDecision)
	}
	result, err := s.decisions.Decide(ctx, identity, scriptuc.DecideReviewCommand{
		ResourceID: req.GetResourceId(), VersionID: req.GetVersionId(), ReviewID: req.GetReviewId(),
		Input: domain.DecisionInput{ExpectedRevision: command.GetExpectedVersionRevision(), PolicyVersion: command.GetPolicyVersion(),
			Action:            map[reviewv1.ScriptReviewAction]string{reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_APPROVE: "APPROVE", reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_REJECT: "REJECT"}[command.GetDecision()],
			ConfirmedCheckIDs: command.GetConfirmedCheckIds(), Reason: command.GetReason()},
	})
	if err != nil {
		return nil, scriptDecisionError(err)
	}
	return &reviewv1.ScriptReviewDecisionReceipt{ReviewId: string(result.ReviewID), VersionRevision: result.Revision,
		Decision: scriptDecisionResponse(&result.Decision), Cascade: scriptCascadeResponse(result.Cascade)}, nil
}

func scriptDecisionResponse(d *domain.Decision) *reviewv1.ScriptReviewDecision {
	if d == nil {
		return nil
	}
	action := reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_APPROVE
	if d.Outcome == domain.VersionRejected {
		action = reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_REJECT
	}
	return &reviewv1.ScriptReviewDecision{DecisionId: d.ID, Decision: action, PolicyVersion: d.PolicyVersion,
		ConfirmedCheckIds: d.ConfirmedCheckIDs, Reason: d.Reason, DecidedBy: d.Actor.String(), DecidedAt: timestamppb.New(d.At)}
}

func scriptCascadeResponse(p *domain.CascadeProgress) *reviewv1.ScriptCascadeProgress {
	if p == nil {
		return nil
	}
	return &reviewv1.ScriptCascadeProgress{TaskId: p.TaskID,
		Status:    reviewv1.ScriptCascadeStatus(reviewv1.ScriptCascadeStatus_value["SCRIPT_CASCADE_STATUS_"+p.Status]),
		Processed: p.Processed, Total: p.Total, LastFailure: p.LastFailure, Rejected: p.Rejected, AlreadyTerminal: p.AlreadyTerminal, ApplicationClosed: p.Closed, ObservedAt: timestamppb.New(p.ObservedAt)}
}

func scriptDecisionError(err error) error {
	for _, mapping := range []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{domain.ErrIdentityRequired, codes.Unauthenticated, "USER_IDENTITY_REQUIRED"},
		{domain.ErrReviewForbidden, codes.PermissionDenied, "SCRIPT_REVIEW_FORBIDDEN"},
		{domain.ErrInvalidResourceID, codes.InvalidArgument, "INVALID_SCRIPT_RESOURCE_ID"},
		{domain.ErrInvalidVersionID, codes.InvalidArgument, "INVALID_SCRIPT_VERSION_ID"},
		{domain.ErrInvalidDecision, codes.InvalidArgument, "INVALID_SCRIPT_REVIEW_REQUEST"},
		{domain.ErrChecksIncomplete, codes.InvalidArgument, "SCRIPT_REVIEW_CHECKS_INCOMPLETE"},
		{domain.ErrReviewConflict, codes.PermissionDenied, "SCRIPT_REVIEW_CONFLICT_OF_INTEREST"},
		{domain.ErrReviewDecided, codes.Aborted, "SCRIPT_REVIEW_ALREADY_DECIDED"},
		{domain.ErrRevisionConflict, codes.Aborted, "SCRIPT_VERSION_REVISION_CONFLICT"},
		{domain.ErrPolicyMismatch, codes.Aborted, "SCRIPT_REVIEW_POLICY_MISMATCH"},
		{port.ErrScriptReviewNotFound, codes.NotFound, "SCRIPT_REVIEW_NOT_FOUND"},
		{domain.ErrVersionNotFound, codes.NotFound, "SCRIPT_REVIEW_NOT_FOUND"},
		{domain.ErrNotFound, codes.NotFound, "SCRIPT_REVIEW_NOT_FOUND"},
		{port.ErrScriptQueryInconsistent, codes.Internal, "SCRIPT_REVIEW_DATA_INCONSISTENT"},
	} {
		if errors.Is(err, mapping.err) {
			return transportStatus(mapping.code, "ERROR_REASON_"+mapping.reason, mapping.err.Error())
		}
	}
	return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "script review operation failed")
}
