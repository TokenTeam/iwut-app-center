package transport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	resourcev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_resource"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_version"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	scriptuc "iwut-app-center/internal/script/usecase"
	"iwut-app-center/internal/shared"
)

type ScriptQueryService struct {
	queryv1.UnimplementedScriptQueryServiceServer
	query *scriptuc.ScriptQuery
}

func NewScriptQueryService(query *scriptuc.ScriptQuery) *ScriptQueryService {
	return &ScriptQueryService{query: query}
}

func scriptQueryIdentity(ctx context.Context, kind scriptuc.ScriptReadKind, request proto.Message) (shared.TrustedIdentity, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	if _, err := scriptuc.ScriptReadAccess(identity, kind); err != nil {
		return shared.TrustedIdentity{}, scriptQueryError(err)
	}
	if !request.ProtoReflect().IsValid() || hasUnknownScriptFields(request) {
		return shared.TrustedIdentity{}, scriptQueryError(scriptuc.ErrInvalidScriptQuery)
	}
	return identity, nil
}

func (s *ScriptQueryService) ListScriptResources(ctx context.Context, req *queryv1.ListScriptResourcesRequest) (*queryv1.ListScriptResourcesResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptResourceRead, req)
	if err != nil {
		return nil, err
	}
	page, err := s.query.ListResources(ctx, identity, req.Limit, req.GetCursor())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	out := &queryv1.ListScriptResourcesResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, scriptResourceResponse(item))
	}
	return out, nil
}

func (s *ScriptQueryService) GetScriptResource(ctx context.Context, req *queryv1.GetScriptResourceRequest) (*resourcev1.ScriptResource, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptResourceRead, req)
	if err != nil {
		return nil, err
	}
	item, err := s.query.GetResource(ctx, identity, req.GetResourceId())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	return scriptResourceResponse(item), nil
}

func (s *ScriptQueryService) ListScriptVersions(ctx context.Context, req *queryv1.ListScriptVersionsRequest) (*queryv1.ListScriptVersionsResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptVersionRead, req)
	if err != nil {
		return nil, err
	}
	state, ok := scriptQueryStates[req.GetState()]
	if !ok {
		return nil, scriptQueryError(scriptuc.ErrInvalidScriptQuery)
	}
	page, err := s.query.ListVersions(ctx, identity, req.GetResourceId(), state, req.Limit, req.GetCursor())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	out := &queryv1.ListScriptVersionsResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, scriptVersionSummaryResponse(item))
	}
	return out, nil
}

func (s *ScriptQueryService) GetScriptVersion(ctx context.Context, req *queryv1.GetScriptVersionRequest) (*queryv1.GetScriptVersionResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptVersionRead, req)
	if err != nil {
		return nil, err
	}
	item, err := s.query.GetVersion(ctx, identity, req.GetResourceId(), req.GetVersionId())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	return &queryv1.GetScriptVersionResponse{Version: scriptVersionSummaryResponse(item.Version), ContentText: item.Text, ReviewId: string(item.ReviewID)}, nil
}

func (s *ScriptQueryService) ListPendingScriptReviews(ctx context.Context, req *queryv1.ListPendingScriptReviewsRequest) (*queryv1.ListPendingScriptReviewsResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptPendingRead, req)
	if err != nil {
		return nil, err
	}
	page, err := s.query.ListPending(ctx, identity, req.GetResourceId(), req.Limit, req.GetCursor())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	out := &queryv1.ListPendingScriptReviewsResponse{NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, scriptPendingSummaryResponse(item))
	}
	return out, nil
}

func (s *ScriptQueryService) GetScriptReview(ctx context.Context, req *queryv1.GetScriptReviewRequest) (*queryv1.GetScriptReviewResponse, error) {
	identity, err := scriptQueryIdentity(ctx, scriptuc.ScriptReviewRead, req)
	if err != nil {
		return nil, err
	}
	result, err := s.query.GetReview(ctx, identity, req.GetResourceId(), req.GetVersionId(), req.GetReviewId())
	if err != nil {
		return nil, scriptQueryError(err)
	}
	review, snapshot := result.Review, result.Review.Snapshot
	out := &queryv1.GetScriptReviewResponse{
		Submission: scriptPendingSummaryResponse(port.ScriptReviewSummary{ResourceID: snapshot.ResourceID, VersionID: snapshot.VersionID,
			ID: review.ID, Label: snapshot.Label, Digest: snapshot.Content.Digest, SubmittedAt: review.SubmittedAt}),
		Status: queryv1.ScriptReviewStatus_SCRIPT_REVIEW_STATUS_PENDING, Version: scriptVersionSummaryResponse(result.Version),
		ContentText: snapshot.Content.Text, SourceRevision: snapshot.SourceRevision, ResourceName: snapshot.ResourceMetadata.Name,
		ResourcePurpose: snapshot.ResourceMetadata.Purpose, ResourceRevision: snapshot.ResourceRevision,
		PolicyVersion: review.Policy.Version, CanDecide: result.CanDecide,
	}
	for _, check := range review.Policy.Checks {
		out.Checks = append(out.Checks, &queryv1.ScriptReviewCheck{Id: check.ID, Title: check.Title, Description: check.Description, Mandatory: check.Mandatory})
	}
	for _, conflict := range result.Conflicts {
		out.Conflicts = append(out.Conflicts, queryv1.ScriptDecisionConflict(queryv1.ScriptDecisionConflict_value["SCRIPT_DECISION_CONFLICT_"+conflict]))
	}
	return out, nil
}

var scriptQueryStates = map[versionv1.ScriptVersionState]domain.VersionState{
	versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_UNSPECIFIED: "",
	versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_DRAFT:       domain.VersionDraft,
	versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_SUBMITTED:   domain.VersionSubmitted,
	versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_APPROVED:    domain.VersionApproved,
	versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_REJECTED:    domain.VersionRejected,
}

func scriptVersionSummaryResponse(v port.ScriptVersionSummary) *queryv1.ScriptVersionSummary {
	state := versionv1.ScriptVersionState(versionv1.ScriptVersionState_value["SCRIPT_VERSION_STATE_"+string(v.State)])
	return &queryv1.ScriptVersionSummary{ResourceId: string(v.ResourceID), VersionId: string(v.ID), VersionLabel: v.Label,
		ContentFormat: v.Format, Digest: v.Digest, State: state, Revision: v.Revision,
		CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt)}
}

func scriptPendingSummaryResponse(review port.ScriptReviewSummary) *queryv1.PendingScriptReviewSummary {
	return &queryv1.PendingScriptReviewSummary{ResourceId: string(review.ResourceID), VersionId: string(review.VersionID),
		ReviewId: string(review.ID), VersionLabel: review.Label, Digest: review.Digest, SubmittedAt: timestamppb.New(review.SubmittedAt)}
}

func scriptQueryError(err error) error {
	switch {
	case errors.Is(err, domain.ErrIdentityRequired):
		return toIdentityTransportError(errIdentityRequired, queryv1.OperationScriptQueryServiceListScriptResources)
	case errors.Is(err, scriptuc.ErrScriptReadForbidden):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_SCRIPT_QUERY_FORBIDDEN", "script read permission is required")
	case errors.Is(err, scriptuc.ErrInvalidScriptQuery), errors.Is(err, domain.ErrInvalidResourceID), errors.Is(err, domain.ErrInvalidVersionID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_QUERY", "invalid script query")
	case errors.Is(err, port.ErrInvalidScriptCursor):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_CURSOR", "invalid script cursor")
	case errors.Is(err, domain.ErrNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_RESOURCE_NOT_FOUND", "script resource not found")
	case errors.Is(err, domain.ErrVersionNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_VERSION_NOT_FOUND", "script version not found")
	case errors.Is(err, port.ErrScriptReviewNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_REVIEW_NOT_FOUND", "script review not found")
	case errors.Is(err, port.ErrScriptQueryInconsistent), errors.Is(err, domain.ErrInternal):
		return transportStatus(codes.Internal, "ERROR_REASON_SCRIPT_QUERY_INCONSISTENT", "script query state is inconsistent")
	default:
		return transportStatus(codes.Unavailable, "ERROR_REASON_SCRIPT_QUERY_UNAVAILABLE", "script query unavailable")
	}
}

func scriptQueryOperation(operation string) bool {
	return strings.HasPrefix(operation, "/app_center.v1.script_query.ScriptQueryService/")
}

func scriptQueryHTTPRoute(r *http.Request) (operation string, kind scriptuc.ScriptReadKind, allowedQuery string) {
	if r.Method != http.MethodGet {
		return "", 0, ""
	}
	if r.URL.Path == "/v1/script-reviews/pending" {
		return queryv1.OperationScriptQueryServiceListPendingScriptReviews, scriptuc.ScriptPendingRead, "limit cursor resourceId resource_id"
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 || parts[1] != "v1" || parts[2] != "script-resources" {
		return "", 0, ""
	}
	if len(parts) == 3 {
		return queryv1.OperationScriptQueryServiceListScriptResources, scriptuc.ScriptResourceRead, "limit cursor"
	}
	if len(parts) == 4 && parts[3] != "" {
		return queryv1.OperationScriptQueryServiceGetScriptResource, scriptuc.ScriptResourceRead, ""
	}
	if len(parts) < 5 || parts[3] == "" || parts[4] != "versions" {
		return "", 0, ""
	}
	switch {
	case len(parts) == 5:
		return queryv1.OperationScriptQueryServiceListScriptVersions, scriptuc.ScriptVersionRead, "limit cursor state"
	case len(parts) == 6 && parts[5] != "":
		return queryv1.OperationScriptQueryServiceGetScriptVersion, scriptuc.ScriptVersionRead, ""
	case len(parts) == 8 && parts[5] != "" && parts[6] == "reviews" && parts[7] != "":
		return queryv1.OperationScriptQueryServiceGetScriptReview, scriptuc.ScriptReviewRead, ""
	default:
		return "", 0, ""
	}
}

func scriptQueryHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op, kind, allowed := scriptQueryHTTPRoute(r)
			if op == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			identity, err := verifyScriptHTTPIdentity(r, verifier)
			if err != nil {
				khttp.DefaultErrorEncoder(w, r, toIdentityTransportError(err, op))
				return
			}
			if _, err := scriptuc.ScriptReadAccess(identity, kind); err != nil {
				khttp.DefaultErrorEncoder(w, r, scriptQueryError(err))
				return
			}
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || (values.Has("resourceId") && values.Has("resource_id")) {
				khttp.DefaultErrorEncoder(w, r, scriptQueryError(scriptuc.ErrInvalidScriptQuery))
				return
			}
			fields := strings.Fields(allowed)
			for field, items := range values {
				if !slices.Contains(fields, field) || len(items) != 1 {
					khttp.DefaultErrorEncoder(w, r, scriptQueryError(scriptuc.ErrInvalidScriptQuery))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func scriptQueryHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := queryv1.ErrorReason_value[converted.Reason]; !ok && converted.Code == http.StatusBadRequest {
		err = scriptQueryError(scriptuc.ErrInvalidScriptQuery)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
