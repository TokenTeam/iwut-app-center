package usecase

import (
	"context"
	"errors"
	"slices"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

var (
	ErrScriptReadForbidden = errors.New("script read permission required")
	ErrInvalidScriptQuery  = errors.New("invalid script query")
)

type ScriptReadKind uint8

const (
	ScriptResourceRead ScriptReadKind = iota
	ScriptVersionRead
	ScriptPendingRead
	ScriptReviewRead
	ScriptPublicationRead
	ScriptPublicationHistoryRead
)

func ScriptReadAccess(identity shared.TrustedIdentity, kind ScriptReadKind) (port.ScriptReadScope, error) {
	if !identity.AuthID.IsValid() {
		return port.ScriptReadScope{}, domain.ErrIdentityRequired
	}
	has := func(permission string) bool { return slices.Contains(identity.Permissions, permission) }
	scope := port.ScriptReadScope{
		Actor: identity.AuthID, Maintain: has(domain.MaintainPermission), Review: has("app.script.review"),
		Publish: has("app.script.publish"), Suspend: has("app.script.suspend"), Restore: has("app.script.restore"),
	}
	var allowed bool
	switch kind {
	case ScriptResourceRead, ScriptPublicationRead:
		allowed = scope.Maintain || scope.Review || scope.Publish || scope.Suspend || scope.Restore
	case ScriptVersionRead:
		allowed = scope.Maintain || scope.Review || scope.Publish
	case ScriptPendingRead:
		allowed = scope.Review
	case ScriptPublicationHistoryRead:
		allowed = scope.Publish
	case ScriptReviewRead:
		allowed = scope.Maintain || scope.Review
	}
	if !allowed {
		return port.ScriptReadScope{}, ErrScriptReadForbidden
	}
	return scope, nil
}

type ScriptQuery struct {
	repo  port.ScriptQueryRepository
	clock port.Clock
}

func NewScriptQuery(repo port.ScriptQueryRepository, clock port.Clock) *ScriptQuery {
	return &ScriptQuery{repo: repo, clock: clock}
}

func scriptPage(limit *int32, cursor string) (port.ScriptPageRequest, error) {
	page := port.ScriptPageRequest{Limit: 20, Cursor: cursor}
	if len(cursor) > 1024 {
		return page, port.ErrInvalidScriptCursor
	}
	if limit != nil {
		if *limit < 1 || *limit > 100 {
			return page, ErrInvalidScriptQuery
		}
		page.Limit = int(*limit)
	}
	return page, nil
}

func (q *ScriptQuery) ListResources(ctx context.Context, identity shared.TrustedIdentity, limit *int32, cursor string) (port.ScriptPage[domain.Resource], error) {
	scope, err := ScriptReadAccess(identity, ScriptResourceRead)
	if err != nil {
		return port.ScriptPage[domain.Resource]{}, err
	}
	page, err := scriptPage(limit, cursor)
	if err != nil {
		return port.ScriptPage[domain.Resource]{}, err
	}
	return q.repo.ListResources(ctx, scope, page)
}

func (q *ScriptQuery) GetResource(ctx context.Context, identity shared.TrustedIdentity, resourceID string) (domain.Resource, error) {
	if _, err := ScriptReadAccess(identity, ScriptResourceRead); err != nil {
		return domain.Resource{}, err
	}
	id, err := domain.ParseResourceID(resourceID)
	if err != nil {
		return domain.Resource{}, err
	}
	return q.repo.GetResource(ctx, id)
}

func (q *ScriptQuery) ListVersions(ctx context.Context, identity shared.TrustedIdentity, resourceID string, state domain.VersionState, limit *int32, cursor string) (port.ScriptPage[port.ScriptVersionSummary], error) {
	var empty port.ScriptPage[port.ScriptVersionSummary]
	scope, err := ScriptReadAccess(identity, ScriptVersionRead)
	if err != nil {
		return empty, err
	}
	id, err := domain.ParseResourceID(resourceID)
	if err != nil {
		return empty, err
	}
	switch state {
	case "", domain.VersionDraft, domain.VersionSubmitted, domain.VersionApproved, domain.VersionRejected:
	default:
		return empty, ErrInvalidScriptQuery
	}
	page, err := scriptPage(limit, cursor)
	if err != nil {
		return empty, err
	}
	return q.repo.ListVersions(ctx, scope, id, state, page)
}

func (q *ScriptQuery) GetVersion(ctx context.Context, identity shared.TrustedIdentity, resourceID, versionID string) (port.ScriptVersionDetail, error) {
	scope, err := ScriptReadAccess(identity, ScriptVersionRead)
	if err != nil {
		return port.ScriptVersionDetail{}, err
	}
	resource, version, err := scriptQueryIDs(resourceID, versionID)
	if err != nil {
		return port.ScriptVersionDetail{}, err
	}
	return q.repo.GetVersion(ctx, scope, resource, version)
}

func (q *ScriptQuery) ListPending(ctx context.Context, identity shared.TrustedIdentity, resourceID string, limit *int32, cursor string) (port.ScriptPage[port.ScriptReviewSummary], error) {
	var empty port.ScriptPage[port.ScriptReviewSummary]
	scope, err := ScriptReadAccess(identity, ScriptPendingRead)
	if err != nil {
		return empty, err
	}
	var id domain.ResourceID
	if resourceID != "" {
		id, err = domain.ParseResourceID(resourceID)
		if err != nil {
			return empty, err
		}
	}
	page, err := scriptPage(limit, cursor)
	if err != nil {
		return empty, err
	}
	return q.repo.ListPendingReviews(ctx, scope, id, page)
}

type ScriptReviewResult struct {
	port.ScriptReviewDetail
	CanDecide bool
	Conflicts []string
}

func (q *ScriptQuery) GetReview(ctx context.Context, identity shared.TrustedIdentity, resourceID, versionID, reviewID string) (ScriptReviewResult, error) {
	scope, err := ScriptReadAccess(identity, ScriptReviewRead)
	if err != nil {
		return ScriptReviewResult{}, err
	}
	resource, version, err := scriptQueryIDs(resourceID, versionID)
	if err != nil {
		return ScriptReviewResult{}, err
	}
	id, valid := domain.ParseReviewID(reviewID)
	if !valid {
		return ScriptReviewResult{}, ErrInvalidScriptQuery
	}
	detail, err := q.repo.GetReview(ctx, resource, version, id)
	if err != nil {
		return ScriptReviewResult{}, err
	}
	result := ScriptReviewResult{ScriptReviewDetail: detail}
	if result.Cascade != nil {
		result.Cascade.ObservedAt = q.clock.Now().UTC()
	}
	contributors := detail.Review.Snapshot.Contributors
	if len(contributors) > 0 && contributors[0] == scope.Actor {
		result.Conflicts = append(result.Conflicts, "VERSION_CREATOR")
	}
	if len(contributors) > 1 && slices.Contains(contributors[1:], scope.Actor) {
		result.Conflicts = append(result.Conflicts, "CONTENT_EDITOR")
	}
	if detail.Review.SubmittedBy == scope.Actor {
		result.Conflicts = append(result.Conflicts, "REVIEW_SUBMITTER")
	}
	result.CanDecide = scope.Review && detail.Review.Status == domain.ReviewPending && len(result.Conflicts) == 0
	return result, nil
}

func scriptQueryIDs(resourceID, versionID string) (domain.ResourceID, domain.VersionID, error) {
	resource, err := domain.ParseResourceID(resourceID)
	if err != nil {
		return "", "", err
	}
	version, err := domain.ParseVersionID(versionID)
	return resource, version, err
}

func (q *ScriptQuery) GetPublication(ctx context.Context, identity shared.TrustedIdentity, resource string) (domain.PublicationState, error) {
	if _, err := ScriptReadAccess(identity, ScriptPublicationRead); err != nil {
		return domain.PublicationState{}, err
	}
	id, err := domain.ParseResourceID(resource)
	if err != nil {
		return domain.PublicationState{}, err
	}
	return q.repo.GetPublication(ctx, id)
}
func (q *ScriptQuery) ListPublicationHistory(ctx context.Context, identity shared.TrustedIdentity, resource string, limit *int32, cursor string) (port.ScriptPage[port.PublicationHistoryItem], error) {
	var empty port.ScriptPage[port.PublicationHistoryItem]
	scope, err := ScriptReadAccess(identity, ScriptPublicationHistoryRead)
	if err != nil {
		return empty, err
	}
	id, err := domain.ParseResourceID(resource)
	if err != nil {
		return empty, err
	}
	page, err := scriptPage(limit, cursor)
	if err != nil {
		return empty, err
	}
	return q.repo.ListPublicationHistory(ctx, scope, id, page)
}
