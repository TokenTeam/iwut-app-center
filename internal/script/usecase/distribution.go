package usecase

import (
	"context"
	"errors"
	"strings"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type ScriptDistribution struct {
	repo  port.ScriptDistributionRepository
	clock port.Clock
}

func NewScriptDistribution(repo port.ScriptDistributionRepository, clock port.Clock) *ScriptDistribution {
	return &ScriptDistribution{repo: repo, clock: clock}
}

func RequireDistributionUser(identity shared.AuthenticatedUserIdentity) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrIdentityRequired
	}
	return nil
}

func distributionFailure(err error) error {
	for _, known := range []error{domain.ErrInvalidDistributionRequest, domain.ErrInvalidDistributionCursor, domain.ErrScriptContentUnavailable, domain.ErrDistributionStateInconsistent, domain.ErrDistributionUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return domain.ErrDistributionUnavailable
}

func (h *ScriptDistribution) ListPublished(ctx context.Context, identity shared.AuthenticatedUserIdentity, limit *int32, cursor string) (domain.PublishedScriptPage, error) {
	var empty domain.PublishedScriptPage
	if err := RequireDistributionUser(identity); err != nil {
		return empty, err
	}
	page, err := scriptPage(limit, cursor)
	if err != nil {
		if errors.Is(err, port.ErrInvalidScriptCursor) {
			return empty, domain.ErrInvalidDistributionCursor
		}
		return empty, domain.ErrInvalidDistributionRequest
	}
	result, err := h.repo.ListPublishedScripts(ctx, identity.AuthID, page, h.clock.Now().UTC())
	if err != nil {
		return empty, distributionFailure(err)
	}
	if result.AsOf.IsZero() || len(result.Items) > page.Limit || len(result.NextCursor) > 1024 {
		return empty, domain.ErrDistributionStateInconsistent
	}
	seen := map[domain.ResourceID]bool{}
	for _, item := range result.Items {
		if item.Validate() != nil || seen[item.ResourceID] {
			return empty, domain.ErrDistributionStateInconsistent
		}
		seen[item.ResourceID] = true
	}
	return result, nil
}

func (h *ScriptDistribution) GetPublishedContent(ctx context.Context, identity shared.AuthenticatedUserIdentity, resource, version, digest string) (domain.ScriptContent, error) {
	var empty domain.ScriptContent
	if err := RequireDistributionUser(identity); err != nil {
		return empty, err
	}
	r, er := domain.ParseResourceID(resource)
	v, ev := domain.ParseVersionID(version)
	if er != nil || ev != nil || !shared.ValidScriptDigest(digest) {
		return empty, domain.ErrInvalidDistributionRequest
	}
	result, err := h.repo.GetPublishedScriptContent(ctx, r, v, digest, h.clock.Now().UTC())
	if err != nil {
		return empty, distributionFailure(err)
	}
	if result.Validate() != nil || result.Descriptor.ResourceID != r || result.Descriptor.VersionID != v || result.Descriptor.Digest != digest {
		return empty, domain.ErrDistributionStateInconsistent
	}
	return result, nil
}

type ResolveScriptManifestQuery struct {
	ApplicationID, ApplicationVersionID string
	Mode                                domain.ScriptRuntimeMode
	RPCAPIMajor                         int32
	HostCapabilities                    []string
}

type GetApplicationScriptContentQuery struct {
	ResolveScriptManifestQuery
	ScriptVersionID, ExpectedDigest string
}

func (h *ScriptDistribution) applicationRequest(identity shared.AuthenticatedUserIdentity, query ResolveScriptManifestQuery) (port.ApplicationScriptRequest, error) {
	if err := RequireDistributionUser(identity); err != nil {
		return port.ApplicationScriptRequest{}, err
	}
	app, valid := shared.ParseApplicationID(query.ApplicationID)
	runtime, err := domain.NewScriptRuntimeQuery(query.Mode, query.RPCAPIMajor, query.HostCapabilities)
	if !valid || !shared.IsUUIDv7(query.ApplicationVersionID) || err != nil {
		return port.ApplicationScriptRequest{}, domain.ErrInvalidDistributionRequest
	}
	return port.ApplicationScriptRequest{Caller: identity.AuthID, ApplicationID: app, ApplicationVersionID: strings.ToLower(query.ApplicationVersionID), Runtime: runtime, ObservedAt: h.clock.Now().UTC()}, nil
}

func matchesRequestedTarget(target domain.ApplicationScriptTarget, request port.ApplicationScriptRequest) bool {
	return target.Validate() == nil && target.ApplicationID == request.ApplicationID && target.ApplicationVersionID == request.ApplicationVersionID && target.RPCAPIMajor == request.Runtime.RPCAPIMajor && (request.Runtime.Mode != domain.ScriptRuntimePureTest || target.Channel == "TEST")
}

func (h *ScriptDistribution) ResolveManifest(ctx context.Context, identity shared.AuthenticatedUserIdentity, query ResolveScriptManifestQuery) (domain.ApplicationScriptManifest, error) {
	var empty domain.ApplicationScriptManifest
	request, err := h.applicationRequest(identity, query)
	if err != nil {
		return empty, err
	}
	result, err := h.repo.ResolveApplicationScriptManifest(ctx, request)
	if err != nil {
		return empty, distributionFailure(err)
	}
	if result.Validate() != nil || !matchesRequestedTarget(result.Target, request) {
		return empty, domain.ErrDistributionStateInconsistent
	}
	return result, nil
}

func (h *ScriptDistribution) GetApplicationContent(ctx context.Context, identity shared.AuthenticatedUserIdentity, query GetApplicationScriptContentQuery) (domain.ApplicationScriptContent, error) {
	var empty domain.ApplicationScriptContent
	request, err := h.applicationRequest(identity, query.ResolveScriptManifestQuery)
	if err != nil {
		return empty, err
	}
	version, err := domain.ParseVersionID(query.ScriptVersionID)
	if err != nil || !shared.ValidScriptDigest(query.ExpectedDigest) {
		return empty, domain.ErrInvalidDistributionRequest
	}
	result, err := h.repo.GetApplicationScriptContent(ctx, request, version, query.ExpectedDigest)
	if err != nil {
		return empty, distributionFailure(err)
	}
	if !matchesRequestedTarget(result.Target, request) || result.Content.Validate() != nil || result.Content.Descriptor.VersionID != version || result.Content.Descriptor.Digest != query.ExpectedDigest {
		return empty, domain.ErrDistributionStateInconsistent
	}
	return result, nil
}
