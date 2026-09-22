package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
	"reflect"
	"testing"
	"time"
)

const app shared.ApplicationID = "01900000-0000-7000-8000-000000000001"
const version domain.ApplicationVersionID = "01900000-0000-7000-8000-000000000002"
const review domain.ApplicationReviewID = "01900000-0000-7000-8000-000000000003"
const publication domain.ApplicationPublicationID = "01900000-0000-7000-8000-000000000004"
const history domain.ApplicationPublicationHistoryID = "01900000-0000-7000-8000-000000000005"
const oldVersion domain.ApplicationVersionID = "01900000-0000-7000-8000-000000000006"

var now = time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
var identity = shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved}

type fixture struct {
	candidate                                     *domain.TestPlacementCandidate
	events                                        []string
	scopes                                        []domain.ScopeName
	url                                           domain.LaunchURL
	loadErr, scopeErr, policyErr, idErr, placeErr error
	scopeRevision                                 domain.ScopeCatalogRevision
	policyVersion                                 domain.PreflightPolicyVersion
	idValue                                       string
	clockValue                                    time.Time
	ids                                           int
}

func (f *fixture) LoadTestPlacementCandidate(_ context.Context, a shared.ApplicationID, m int32, v domain.ApplicationVersionID, admin shared.AuthID, rev *int64) (*domain.TestPlacementCandidate, error) {
	f.events = append(f.events, "load")
	return f.candidate, f.loadErr
}
func (f *fixture) PlaceInTest(_ context.Context, c *domain.TestPlacementCandidate, p *domain.ApplicationPublicationID, h domain.ApplicationPublicationHistoryID, admin shared.AuthID, v domain.PublicationValidation, at time.Time) (*domain.PlaceInTestResult, error) {
	f.events = append(f.events, "place")
	if f.placeErr != nil {
		return nil, f.placeErr
	}
	return c.PlaceInTest(p, h, admin, v, at)
}
func (f *fixture) EnsureAllRequestable(_ context.Context, s []domain.ScopeName) (domain.ScopeCatalogRevision, error) {
	f.events = append(f.events, "scope")
	f.scopes = s
	return f.scopeRevision, f.scopeErr
}
func (f *fixture) Inspect(_ context.Context, u domain.LaunchURL) (domain.PreflightPolicyVersion, error) {
	f.events = append(f.events, "url")
	f.url = u
	return f.policyVersion, f.policyErr
}
func (f *fixture) NewUUIDv7() (string, error) {
	f.events = append(f.events, "id")
	f.ids++
	if f.idErr != nil {
		return "", f.idErr
	}
	if f.idValue != "" {
		return f.idValue, nil
	}
	if f.candidate.Publication() == nil && f.ids == 1 {
		return publication.String(), nil
	}
	return history.String(), nil
}
func (f *fixture) Now() time.Time { f.events = append(f.events, "clock"); return f.clockValue }
func setup(t *testing.T, existing domain.ApplicationVersionID) *fixture {
	t.Helper()
	s, e := domain.NewApplicationVersionReviewSnapshot("1.0", "https://iwut.net/app", 3, 5, []string{}, []domain.ScopeName{"b"}, []domain.ScopeName{"a"})
	if e != nil {
		t.Fatal(e)
	}
	var p *domain.ApplicationPublication
	var rev *int64
	if existing != "" {
		p, e = domain.RestoreApplicationPublication(publication, app, 3, existing, 1, "admin", now, "admin", now)
		if e != nil {
			t.Fatal(e)
		}
		v := int64(1)
		rev = &v
	}
	c, e := domain.NewTestPlacementCandidate(app, 3, version, review, 3, *s, p, rev)
	if e != nil {
		t.Fatal(e)
	}
	return &fixture{candidate: c, scopeRevision: 19, policyVersion: "submit-v1", clockValue: now}
}
func run(f *fixture) (*domain.PlaceInTestResult, error) {
	return NewPlaceApprovedVersionInTestSlotHandler(f, f, f, f, f).Handle(context.Background(), identity, app, 3, PlaceApprovedVersionInTestSlotCommand{version, f.candidate.ExpectedPublicationRevision()})
}
func TestBRPUB006007008CreateReplaceAndNoOp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing domain.ApplicationVersionID
		events   []string
		changed  bool
		revision int64
	}{
		{"create", "", []string{"load", "scope", "url", "id", "id", "clock", "place"}, true, 1},
		{"replace", oldVersion, []string{"load", "scope", "url", "id", "clock", "place"}, true, 2},
		{"no_op", version, []string{"load"}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, tc.existing)
			r, e := run(f)
			if e != nil {
				t.Fatal(e)
			}
			if r.Changed() != tc.changed || r.Publication().Revision() != tc.revision || !reflect.DeepEqual(f.events, tc.events) {
				t.Fatalf("bad result or calls: %v %#v", r, f.events)
			}
			if tc.changed && (!reflect.DeepEqual(f.scopes, []domain.ScopeName{"a", "b"}) || f.url != "https://iwut.net/app" || r.History().ScopeCatalogRevision() != 19) {
				t.Fatal("approved snapshot not checked")
			}
		})
	}
}
func TestBRPUB001002006InvalidCommandHasNoCalls(t *testing.T) {
	zero := int64(0)
	for _, tc := range []struct {
		name     string
		identity shared.DeveloperIdentity
		app      shared.ApplicationID
		major    int32
		command  PlaceApprovedVersionInTestSlotCommand
		want     error
	}{
		{"missing_identity", shared.DeveloperIdentity{}, app, 3, PlaceApprovedVersionInTestSlotCommand{VersionID: version}, domain.ErrDeveloperIdentityRequired},
		{"suspended", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusSuspended}, app, 3, PlaceApprovedVersionInTestSlotCommand{VersionID: version}, domain.ErrDeveloperApprovalRequired},
		{"major", identity, app, 0, PlaceApprovedVersionInTestSlotCommand{VersionID: version}, domain.ErrInvalidRpcApiMajor},
		{"version", identity, app, 3, PlaceApprovedVersionInTestSlotCommand{VersionID: "bad"}, domain.ErrInvalidApplicationVersionId},
		{"revision", identity, app, 3, PlaceApprovedVersionInTestSlotCommand{version, &zero}, domain.ErrInvalidApplicationPublicationRevision},
		{"application", identity, "bad", 3, PlaceApprovedVersionInTestSlotCommand{VersionID: version}, domain.ErrApplicationVersionNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, "")
			_, e := NewPlaceApprovedVersionInTestSlotHandler(f, f, f, f, f).Handle(context.Background(), tc.identity, tc.app, tc.major, tc.command)
			if !errors.Is(e, tc.want) || len(f.events) != 0 {
				t.Fatalf("%v %v", e, f.events)
			}
		})
	}
}
func TestBRPUB008009DependencyFailureDoesNotWrite(t *testing.T) {
	boom := errors.New("sensitive dependency details")
	for _, tc := range []struct {
		name   string
		change func(*fixture)
		want   error
	}{
		{"scope_denied", func(f *fixture) { f.scopeErr = port.ErrScopeNotRequestable }, domain.ErrInvalidApplicationScope},
		{"scope_unavailable", func(f *fixture) { f.scopeErr = port.ErrScopeCatalogUnavailable }, domain.ErrScopeCatalogUnavailable},
		{"scope_error", func(f *fixture) { f.scopeErr = boom }, domain.ErrInternal},
		{"scope_invalid_revision", func(f *fixture) { f.scopeRevision = 0 }, domain.ErrInternal},
		{"url_denied", func(f *fixture) { f.policyErr = port.ErrLaunchURLNotReviewable }, domain.ErrApplicationLaunchURLNotReviewable},
		{"url_unavailable", func(f *fixture) { f.policyErr = port.ErrLaunchURLInspectionUnavailable }, domain.ErrLaunchURLInspectionUnavailable},
		{"url_error", func(f *fixture) { f.policyErr = boom }, domain.ErrInternal},
		{"url_invalid_version", func(f *fixture) { f.policyVersion = "bad version" }, domain.ErrInternal},
		{"id_error", func(f *fixture) { f.idErr = boom }, domain.ErrInternal},
		{"id_invalid", func(f *fixture) { f.idValue = "bad" }, domain.ErrInternal},
		{"clock_invalid", func(f *fixture) { f.clockValue = time.Time{} }, domain.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, "")
			tc.change(f)
			r, e := run(f)
			if r != nil || !errors.Is(e, tc.want) {
				t.Fatalf("%v %v", r, e)
			}
			for _, event := range f.events {
				if event == "place" {
					t.Fatal("failure persisted")
				}
			}
		})
	}
}
func TestBRPUB001003006009RepositoryFailuresStableAndNoOpEligibility(t *testing.T) {
	for _, pair := range []struct{ source, want error }{
		{port.ErrApplicationVersionNotFound, domain.ErrApplicationVersionNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired}, {port.ErrApplicationVersionNotApproved, domain.ErrApplicationVersionNotApproved}, {port.ErrApplicationReviewStateInconsistent, domain.ErrApplicationReviewStateInconsistent}, {port.ErrApplicationVersionRpcApiIncompatible, domain.ErrApplicationVersionRpcApiIncompatible}, {port.ErrApplicationPublicationAlreadyExists, domain.ErrApplicationPublicationAlreadyExists}, {port.ErrApplicationPublicationNotFound, domain.ErrApplicationPublicationNotFound}, {port.ErrApplicationPublicationRevisionConflict, domain.ErrApplicationPublicationRevisionConflict},
	} {
		t.Run(pair.source.Error(), func(t *testing.T) {
			for _, noOp := range []bool{false, true} {
				f := setup(t, "")
				if noOp {
					f = setup(t, version)
				}
				f.loadErr = pair.source
				r, e := run(f)
				if r != nil || !errors.Is(e, pair.want) || !reflect.DeepEqual(f.events, []string{"load"}) {
					t.Fatalf("%v %v %v", r, e, f.events)
				}
			}
			f := setup(t, "")
			f.placeErr = pair.source
			r, e := run(f)
			if r != nil || !errors.Is(e, pair.want) {
				t.Fatalf("final error %v %v", r, e)
			}
		})
	}
}
func TestBRPUB006NoOpNeedsNoExternalDependencies(t *testing.T) {
	f := setup(t, version)
	r, e := NewPlaceApprovedVersionInTestSlotHandler(nil, nil, nil, nil, f).Handle(context.Background(), identity, app, 3, PlaceApprovedVersionInTestSlotCommand{version, f.candidate.ExpectedPublicationRevision()})
	if e != nil || r.Changed() {
		t.Fatalf("%v %v", r, e)
	}
}
