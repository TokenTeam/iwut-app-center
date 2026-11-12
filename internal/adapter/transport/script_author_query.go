package transport

import (
	"context"
	"errors"
	"net/http"

	authoringv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_authoring"
	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_review"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	scriptuc "iwut-app-center/internal/script/usecase"
	"iwut-app-center/internal/shared"
)

const (
	listOwnScriptResources = authoringv1.OperationScriptAuthoringServiceListOwnScriptResources
	getOwnScriptResource   = authoringv1.OperationScriptAuthoringServiceGetOwnScriptResource
	listOwnScriptVersions  = authoringv1.OperationScriptAuthoringServiceListOwnScriptVersions
	getOwnScriptVersion    = authoringv1.OperationScriptAuthoringServiceGetOwnScriptVersion
	getOwnScriptReview     = authoringv1.OperationScriptAuthoringServiceGetOwnScriptReviewResult
)

func isOwnScriptQuery(operation string) bool {
	switch operation {
	case listOwnScriptResources, getOwnScriptResource, listOwnScriptVersions, getOwnScriptVersion, getOwnScriptReview:
		return true
	default:
		return false
	}
}

func ownScriptQueryRoute(parts []string) (string, scriptuc.ScriptReadKind, string) {
	switch {
	case len(parts) == 0:
		return listOwnScriptResources, scriptuc.ScriptOwnRead, "limit cursor"
	case parts[0] == "":
		return "", 0, ""
	case len(parts) == 1:
		return getOwnScriptResource, scriptuc.ScriptOwnRead, ""
	case parts[1] != "versions":
		return "", 0, ""
	case len(parts) == 2:
		return listOwnScriptVersions, scriptuc.ScriptOwnRead, "limit cursor state"
	case len(parts) == 3 && parts[2] != "":
		return getOwnScriptVersion, scriptuc.ScriptOwnRead, ""
	case len(parts) == 5 && parts[2] != "" && parts[3] == "reviews" && parts[4] != "":
		return getOwnScriptReview, scriptuc.ScriptOwnRead, ""
	default:
		return "", 0, ""
	}
}

func scriptQueryActor(ctx context.Context, request proto.Message, operation string) (shared.AuthID, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	if !identity.AuthID.IsValid() {
		return "", toIdentityTransportError(errIdentityRequired, operation)
	}
	if request == nil || !request.ProtoReflect().IsValid() || hasUnknownScriptFields(request) {
		return "", ownScriptQueryError(operation, scriptuc.ErrInvalidScriptQuery)
	}
	return identity.AuthID, nil
}

func (s *ScriptManagementService) ListOwnScriptResources(ctx context.Context, req *queryv1.ListScriptResourcesRequest) (*authoringv1.ListOwnScriptResourcesResponse, error) {
	actor, err := scriptQueryActor(ctx, req, listOwnScriptResources)
	if err != nil {
		return nil, err
	}
	page, err := s.query.ListOwnResources(ctx, actor, req.Limit, req.GetCursor())
	if err != nil {
		return nil, ownScriptQueryError(listOwnScriptResources, err)
	}
	out := &authoringv1.ListOwnScriptResourcesResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, ownScriptResourceResponse(item))
	}
	return out, nil
}

func (s *ScriptManagementService) GetOwnScriptResource(ctx context.Context, req *queryv1.GetScriptResourceRequest) (*authoringv1.OwnScriptResource, error) {
	actor, err := scriptQueryActor(ctx, req, getOwnScriptResource)
	if err != nil {
		return nil, err
	}
	resource, err := s.query.GetOwnResource(ctx, actor, req.GetResourceId())
	if err != nil {
		return nil, ownScriptQueryError(getOwnScriptResource, err)
	}
	return ownScriptResourceResponse(resource), nil
}

func ownScriptResourceResponse(item port.OwnedScriptResource) *authoringv1.OwnScriptResource {
	resource := item.Resource
	out := &authoringv1.OwnScriptResource{
		ResourceId: string(resource.ID), Name: resource.Metadata.Name, Purpose: resource.Metadata.Purpose,
		Revision: resource.Revision, CreatedAt: timestamppb.New(resource.CreatedAt), UpdatedAt: timestamppb.New(resource.UpdatedAt),
	}
	if item.LatestVersion != nil {
		out.LatestVersion = scriptVersionSummaryResponse(*item.LatestVersion)
	}
	return out
}

func (s *ScriptManagementService) ListOwnScriptVersions(ctx context.Context, req *queryv1.ListScriptVersionsRequest) (*queryv1.ListScriptVersionsResponse, error) {
	actor, err := scriptQueryActor(ctx, req, listOwnScriptVersions)
	if err != nil {
		return nil, err
	}
	state, ok := scriptQueryStates[req.GetState()]
	if !ok {
		return nil, ownScriptQueryError(listOwnScriptVersions, scriptuc.ErrInvalidScriptQuery)
	}
	page, err := s.query.ListOwnVersions(ctx, actor, req.GetResourceId(), state, req.Limit, req.GetCursor())
	if err != nil {
		return nil, ownScriptQueryError(listOwnScriptVersions, err)
	}
	out := &queryv1.ListScriptVersionsResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, scriptVersionSummaryResponse(item))
	}
	return out, nil
}

func (s *ScriptManagementService) GetOwnScriptVersion(ctx context.Context, req *queryv1.GetScriptVersionRequest) (*authoringv1.GetOwnScriptVersionResponse, error) {
	actor, err := scriptQueryActor(ctx, req, getOwnScriptVersion)
	if err != nil {
		return nil, err
	}
	version, err := s.query.GetOwnVersion(ctx, actor, req.GetResourceId(), req.GetVersionId())
	if err != nil {
		return nil, ownScriptQueryError(getOwnScriptVersion, err)
	}
	if version.Text == nil {
		return nil, ownScriptQueryError(getOwnScriptVersion, port.ErrScriptQueryInconsistent)
	}
	return &authoringv1.GetOwnScriptVersionResponse{Version: scriptVersionSummaryResponse(version.Version), ContentText: *version.Text}, nil
}

func (s *ScriptManagementService) GetOwnScriptReviewResult(ctx context.Context, req *queryv1.GetScriptReviewRequest) (*authoringv1.OwnScriptReviewResult, error) {
	actor, err := scriptQueryActor(ctx, req, getOwnScriptReview)
	if err != nil {
		return nil, err
	}
	r, err := s.query.GetOwnReview(ctx, actor, req.GetResourceId(), req.GetVersionId(), req.GetReviewId())
	if err != nil {
		return nil, ownScriptQueryError(getOwnScriptReview, err)
	}
	out := &authoringv1.OwnScriptReviewResult{
		ResourceId: string(r.ResourceID), VersionId: string(r.VersionID), ReviewId: string(r.ID),
		Status:      queryv1.ScriptReviewStatus(queryv1.ScriptReviewStatus_value["SCRIPT_REVIEW_STATUS_"+r.Status]),
		SubmittedAt: timestamppb.New(r.SubmittedAt), ResourceName: r.Metadata.Name, ResourcePurpose: r.Metadata.Purpose,
		ResourceRevision: r.ResourceRevision, SourceVersionRevision: r.SourceRevision, ContentFormat: r.Format, Digest: r.Digest, PolicyVersion: r.PolicyVersion,
	}
	if r.DecisionID != "" {
		action := reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_APPROVE
		if r.Outcome == string(domain.VersionRejected) {
			action = reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_REJECT
		}
		out.Decision = &authoringv1.OwnScriptReviewDecision{DecisionId: r.DecisionID, Decision: action, DecidedAt: timestamppb.New(r.DecidedAt), Reason: r.Reason}
	}
	return out, nil
}

func ownScriptQueryError(operation string, err error) error {
	switch {
	case errors.Is(err, domain.ErrIdentityRequired):
		return toIdentityTransportError(errIdentityRequired, operation)
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrVersionNotFound), errors.Is(err, port.ErrScriptReviewNotFound):
		switch operation {
		case getOwnScriptVersion:
			return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_VERSION_NOT_FOUND", "script version not found")
		case getOwnScriptReview:
			return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_REVIEW_NOT_FOUND", "script review not found")
		default:
			return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_RESOURCE_NOT_FOUND", "script resource not found")
		}
	case errors.Is(err, domain.ErrInvalidResourceID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_ID", "invalid script resource id")
	case errors.Is(err, domain.ErrInvalidVersionID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_VERSION_ID", "invalid script version id")
	case errors.Is(err, scriptuc.ErrInvalidScriptQuery):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_AUTHORING_REQUEST", "invalid script authoring request")
	case errors.Is(err, port.ErrInvalidScriptCursor):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_AUTHORING_CURSOR", "invalid script authoring cursor")
	case errors.Is(err, port.ErrScriptQueryInconsistent), errors.Is(err, domain.ErrInternal):
		return transportStatus(codes.Internal, "ERROR_REASON_SCRIPT_AUTHORING_STATE_INCONSISTENT", "script authoring state is inconsistent")
	default:
		return transportStatus(codes.Unavailable, "ERROR_REASON_SCRIPT_AUTHORING_UNAVAILABLE", "script authoring query unavailable")
	}
}

func validOwnScriptQueryBody(r *http.Request) bool {
	return r.ContentLength == 0 && len(r.TransferEncoding) == 0 && !r.URL.ForceQuery
}
