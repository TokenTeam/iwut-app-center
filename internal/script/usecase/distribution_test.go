package usecase

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const distributionAppID = "0194a000-0000-7000-8000-000000000001"
const distributionAppVersionID = "0194a000-0000-7000-8000-000000000002"
const distributionResourceID = "0194a000-0000-7000-8000-000000000003"
const distributionVersionID = "0194a000-0000-7000-8000-000000000004"

type distributionTestClock struct{ at time.Time }

func (c distributionTestClock) Now() time.Time { return c.at }

type distributionRepoStub struct {
	calls      int
	request    port.ApplicationScriptRequest
	content    domain.ScriptContent
	manifest   domain.ApplicationScriptManifest
	appContent domain.ApplicationScriptContent
}

func (r *distributionRepoStub) ListPublishedScripts(_ context.Context, _ shared.AuthID, _ port.ScriptPageRequest, at time.Time) (domain.PublishedScriptPage, error) {
	r.calls++
	return domain.PublishedScriptPage{Items: []domain.PublishedScriptItem{}, AsOf: at}, nil
}
func (r *distributionRepoStub) GetPublishedScriptContent(context.Context, domain.ResourceID, domain.VersionID, string, time.Time) (domain.ScriptContent, error) {
	r.calls++
	return r.content, nil
}
func (r *distributionRepoStub) ResolveApplicationScriptManifest(_ context.Context, request port.ApplicationScriptRequest) (domain.ApplicationScriptManifest, error) {
	r.calls++
	r.request = request
	return r.manifest, nil
}
func (r *distributionRepoStub) GetApplicationScriptContent(_ context.Context, request port.ApplicationScriptRequest, _ domain.VersionID, _ string) (domain.ApplicationScriptContent, error) {
	r.calls++
	r.request = request
	return r.appContent, nil
}

func TestScriptDistribution_UCAPP047_BRSCR021022_IdentityInputAndPortBoundary(t *testing.T) {
	at := time.Now().UTC()
	content, _ := domain.NewContent(domain.JSONFormat, "\ufeffopaque\r\n")
	descriptor := domain.ScriptDescriptor{ResourceID: distributionResourceID, VersionID: distributionVersionID, Format: content.Format, Digest: content.Digest, ByteLength: int64(len(content.Text))}
	target := domain.ApplicationScriptTarget{ApplicationID: distributionAppID, ApplicationVersionID: distributionAppVersionID, PublicationRevision: 4, Channel: "TEST", RPCAPIMajor: 1}
	repo := &distributionRepoStub{content: domain.ScriptContent{Descriptor: descriptor, Text: content.Text, AsOf: at}, manifest: domain.ApplicationScriptManifest{Target: target, Scripts: []domain.ScriptDescriptor{}, AsOf: at}}
	repo.appContent = domain.ApplicationScriptContent{Target: target, Content: repo.content}
	h := NewScriptDistribution(repo, distributionTestClock{at})
	user := shared.AuthenticatedUserIdentity{AuthID: "ordinary-user-without-roles"}
	if _, err := h.ListPublished(t.Context(), shared.AuthenticatedUserIdentity{}, nil, strings.Repeat("!", 2000)); !errors.Is(err, domain.ErrIdentityRequired) || repo.calls != 0 {
		t.Fatal("identity did not precede query parsing", err)
	}
	if page, err := h.ListPublished(t.Context(), user, nil, ""); err != nil || page.Items == nil {
		t.Fatal("ordinary USER cannot read library", err)
	}
	if got, err := h.GetPublishedContent(t.Context(), user, distributionResourceID, distributionVersionID, content.Digest); err != nil || got.Text != content.Text {
		t.Fatal("ordinary USER cannot get independent content", err)
	}
	query := ResolveScriptManifestQuery{ApplicationID: strings.ToUpper(distributionAppID), ApplicationVersionID: strings.ToUpper(distributionAppVersionID), Mode: domain.ScriptRuntimePureTest, RPCAPIMajor: 1, HostCapabilities: []string{"z.v1", "a.v2", "z.v1"}}
	if _, err := h.ResolveManifest(t.Context(), user, query); err != nil || repo.request.ApplicationID.String() != distributionAppID || !slices.Equal(repo.request.Runtime.Capabilities, []string{"a.v2", "z.v1"}) {
		t.Fatal("runtime normalization or mode", err)
	}
	for _, mutate := range []func(*ResolveScriptManifestQuery){func(q *ResolveScriptManifestQuery) { q.Mode = "" }, func(q *ResolveScriptManifestQuery) { q.Mode = "OTHER" }, func(q *ResolveScriptManifestQuery) { q.RPCAPIMajor = 0 }, func(q *ResolveScriptManifestQuery) { q.HostCapabilities = []string{"Camera.v1"} }} {
		bad := query
		mutate(&bad)
		calls := repo.calls
		if _, err := h.ResolveManifest(t.Context(), user, bad); !errors.Is(err, domain.ErrInvalidDistributionRequest) || repo.calls != calls {
			t.Fatal("invalid runtime reached repository", err)
		}
	}
	repo.manifest.Target.ApplicationVersionID = distributionVersionID
	if _, err := h.ResolveManifest(t.Context(), user, query); !errors.Is(err, domain.ErrDistributionStateInconsistent) {
		t.Fatal("wrong application target escaped the port boundary", err)
	}
	repo.content.Text = "changed bytes"
	if _, err := h.GetPublishedContent(t.Context(), user, distributionResourceID, distributionVersionID, content.Digest); !errors.Is(err, domain.ErrDistributionStateInconsistent) {
		t.Fatal("digest mismatch escaped the port boundary", err)
	}
	repo.appContent.Content.Descriptor.VersionID = domain.VersionID(distributionAppVersionID)
	if _, err := h.GetApplicationContent(t.Context(), user, GetApplicationScriptContentQuery{ResolveScriptManifestQuery: query, ScriptVersionID: distributionVersionID, ExpectedDigest: content.Digest}); !errors.Is(err, domain.ErrDistributionStateInconsistent) {
		t.Fatal("unrequested script escaped the port boundary", err)
	}
}
