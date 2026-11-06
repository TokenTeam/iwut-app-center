package transport

import (
	"context"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review_query"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	scriptqueryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
)

func scriptReferencesFromRequest(input *versionv1.ScriptReferences) ([]shared.ScriptReference, error) {
	if input == nil || len(input.Items) > shared.MaxScriptReferences || len(input.ProtoReflect().GetUnknown()) != 0 {
		return nil, shared.ErrInvalidScriptReferences
	}
	items := make([]shared.ScriptReference, len(input.Items))
	for i, ref := range input.Items {
		if ref == nil || len(ref.ProtoReflect().GetUnknown()) != 0 {
			return nil, shared.ErrInvalidScriptReferences
		}
		items[i] = shared.ScriptReference{ResourceID: ref.ScriptResourceId, VersionID: ref.ScriptVersionId}
	}
	return items, nil
}

func scriptReferencesResponse(refs []shared.ScriptReference) *versionv1.ScriptReferences {
	items := make([]*versionv1.ScriptReference, len(refs))
	for i, ref := range refs {
		items[i] = scriptReferenceResponse(ref)
	}
	return &versionv1.ScriptReferences{Items: items}
}

func scriptReferenceResponse(ref shared.ScriptReference) *versionv1.ScriptReference {
	return &versionv1.ScriptReference{ScriptResourceId: ref.ResourceID, ScriptVersionId: ref.VersionID}
}

func scriptDependencyResponse(s shared.ScriptDependencySummary) *versionv1.ApplicationScriptDependency {
	return &versionv1.ApplicationScriptDependency{Reference: scriptReferenceResponse(s.ScriptReference), Format: s.Format, Digest: s.Digest,
		State: s.State, ScriptReviewId: s.ReviewID, ScriptDecisionId: s.DecisionID, NeedsReplacement: s.State == "REJECTED"}
}

func scriptDependencyResponses(summaries []shared.ScriptDependencySummary) []*versionv1.ApplicationScriptDependency {
	out := make([]*versionv1.ApplicationScriptDependency, len(summaries))
	for i, s := range summaries {
		out[i] = scriptDependencyResponse(s)
	}
	return out
}

func (s *ApplicationVersionService) ListApplicationScriptCandidates(ctx context.Context, request *versionv1.ListApplicationScriptCandidatesRequest) (*versionv1.ListApplicationScriptCandidatesResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(versiondomain.ErrDeveloperIdentityRequired)
	}
	if s == nil || s.queryHandler == nil || request == nil {
		return nil, toTransportError(versiondomain.ErrInvalidApplicationVersionQuery)
	}
	result, err := s.queryHandler.ListScriptCandidates(ctx, shared.DeveloperIdentity{AuthID: identity.AuthID, DeveloperStatus: identity.DeveloperStatus},
		request.ApplicationId, request.Limit, request.Cursor)
	if err != nil {
		return nil, toTransportError(err)
	}
	response := &versionv1.ListApplicationScriptCandidatesResponse{NextCursor: result.NextCursor}
	for _, item := range result.Items {
		response.Items = append(response.Items, &versionv1.ApplicationScriptCandidate{Dependency: scriptDependencyResponse(item.Dependency),
			ResourceName: item.ResourceName, ResourcePurpose: item.ResourcePurpose, VersionLabel: item.VersionLabel})
	}
	return response, nil
}

func (s *ApplicationReviewQueryService) GetApplicationReviewScript(ctx context.Context, request *queryv1.GetApplicationReviewScriptRequest) (*queryv1.ApplicationReviewScriptDetail, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(reviewdomain.ErrReviewerIdentityRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(reviewdomain.ErrApplicationReviewNotFound)
	}
	detail, err := s.handler.GetScript(ctx, identity, request.ApplicationId, request.VersionId, request.ReviewId, request.ScriptVersionId)
	if err != nil {
		return nil, toTransportError(err)
	}
	response := &queryv1.ApplicationReviewScriptDetail{Dependency: scriptDependencyResponse(detail.Dependency), ContentText: detail.Text, PolicyVersion: detail.PolicyVersion}
	for _, check := range detail.Checks {
		response.Checks = append(response.Checks, &scriptqueryv1.ScriptReviewCheck{Id: check.ID, Title: check.Title, Description: check.Description, Mandatory: check.Mandatory})
	}
	return response, nil
}
