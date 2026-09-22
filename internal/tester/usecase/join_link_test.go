package usecase

import (
	"context"
	"errors"
	"fmt"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"reflect"
	"strings"
	"testing"
	"time"
)

const app shared.ApplicationID = "01900000-0000-7000-8000-000000000001"
const first domain.ApplicationTesterJoinLinkID = "01900000-0000-7000-8000-000000000002"
const second domain.ApplicationTesterJoinLinkID = "01900000-0000-7000-8000-000000000003"

var now = time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
var identity = shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved}

type fixture struct {
	candidate                                  *domain.TesterJoinLinkCandidate
	events                                     []string
	loadErr, tokenErr, idErr, urlErr, writeErr error
	id                                         string
	at                                         time.Time
	url                                        string
	stored                                     *domain.ApplicationTesterJoinLink
	raw                                        string
	hash                                       [32]byte
	expected                                   *domain.ApplicationTesterJoinLinkID
	builtID                                    domain.ApplicationTesterJoinLinkID
}

func (f *fixture) LoadCurrent(_ context.Context, application shared.ApplicationID, admin shared.AuthID) (*domain.TesterJoinLinkCandidate, error) {
	f.events = append(f.events, "load")
	return f.candidate, f.loadErr
}
func (f *fixture) NewToken() (string, [32]byte, error) {
	f.events = append(f.events, "token")
	return f.raw, f.hash, f.tokenErr
}
func (f *fixture) NewUUIDv7() (string, error) {
	f.events = append(f.events, "id")
	return f.id, f.idErr
}
func (f *fixture) Now() time.Time { f.events = append(f.events, "clock"); return f.at }
func (f *fixture) Build(id domain.ApplicationTesterJoinLinkID, raw string) (string, error) {
	f.events = append(f.events, "url")
	if raw != f.raw {
		return "", errors.New("wrong raw token")
	}
	f.builtID = id
	return f.url, f.urlErr
}
func (f *fixture) CreateOrRotate(_ context.Context, application shared.ApplicationID, admin shared.AuthID, expected *domain.ApplicationTesterJoinLinkID, link *domain.ApplicationTesterJoinLink) (*domain.CreateOrRotateTesterJoinLinkResult, error) {
	f.events = append(f.events, "write")
	f.expected = expected
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.stored = link
	return domain.NewCreateOrRotateTesterJoinLinkResult(link, expected)
}
func setup(t *testing.T, existing bool) *fixture {
	t.Helper()
	var link *domain.ApplicationTesterJoinLink
	var err error
	if existing {
		link, err = domain.NewActiveTesterJoinLink(first, app, domain.NewTesterJoinTokenHash([32]byte{1}), "admin", now)
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := domain.NewTesterJoinLinkCandidate(app, link)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{candidate: c, id: second.String(), at: now, url: "https://app.example/join#joinLinkId=" + second.String() + "&secret=secret-marker", raw: "secret-marker", hash: [32]byte{2}}
}
func run(f *fixture, expected *domain.ApplicationTesterJoinLinkID) (*CreateOrRotateTesterJoinLinkResult, error) {
	return NewCreateOrRotateTesterJoinLinkHandler(f, f, f, f, f).Handle(context.Background(), identity, app, CreateOrRotateTesterJoinLinkCommand{expected})
}
func ptr[T any](v T) *T { return &v }
func TestBRTST004005007008009CreateAndRotateSensitiveResult(t *testing.T) {
	for _, existing := range []bool{false, true} {
		f := setup(t, existing)
		var expected *domain.ApplicationTesterJoinLinkID
		if existing {
			expected = ptr(first)
		}
		r, err := run(f, expected)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.events, []string{"load", "token", "id", "clock", "url", "write"}) || r.JoinURL() != f.url || !equalIDs(r.ReplacedJoinLinkID(), expected) || f.stored.TokenHash().Bytes() != f.hash || f.stored.ApplicationID() != app || f.stored.Status() != domain.JoinLinkStatusActive || f.builtID != second {
			t.Fatal("wrong command sequence or persistence projection")
		}
		for _, value := range []any{r, *r} {
			for _, format := range []string{"%v", "%+v", "%#v"} {
				text := fmt.Sprintf(format, value)
				if strings.Contains(text, f.raw) || strings.Contains(text, f.url) || !strings.Contains(text, "redacted") {
					t.Fatal("sensitive result logged")
				}
			}
		}
	}
}
func TestBRTST001InvalidIdentityAndInputHaveNoEffects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		who      shared.DeveloperIdentity
		app      shared.ApplicationID
		expected *domain.ApplicationTesterJoinLinkID
		want     error
	}{
		{"missing_identity", shared.DeveloperIdentity{}, app, nil, domain.ErrDeveloperIdentityRequired},
		{"pending", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, app, nil, domain.ErrDeveloperApprovalRequired},
		{"missing_developer_status", shared.DeveloperIdentity{AuthID: "admin"}, app, nil, domain.ErrDeveloperApprovalRequired},
		{"invalid_app", identity, "bad", nil, domain.ErrInvalidApplicationId},
		{"invalid_expected", identity, app, ptr(domain.ApplicationTesterJoinLinkID("bad")), domain.ErrInvalidTesterJoinLinkId},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, false)
			r, err := NewCreateOrRotateTesterJoinLinkHandler(f, f, f, f, f).Handle(context.Background(), tc.who, tc.app, CreateOrRotateTesterJoinLinkCommand{tc.expected})
			if r != nil || !errors.Is(err, tc.want) || len(f.events) != 0 {
				t.Fatal("invalid request had side effects or wrong error")
			}
		})
	}
}
func TestBRTST005006ExpectedMismatchGeneratesNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
		expected *domain.ApplicationTesterJoinLinkID
		want     error
	}{
		{"exists", true, nil, domain.ErrApplicationTesterJoinLinkAlreadyExists},
		{"missing", false, ptr(first), domain.ErrApplicationTesterJoinLinkNotFound},
		{"changed", true, ptr(second), domain.ErrApplicationTesterJoinLinkChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, tc.existing)
			r, err := run(f, tc.expected)
			if r != nil || !errors.Is(err, tc.want) || !reflect.DeepEqual(f.events, []string{"load"}) {
				t.Fatal("mismatch generated or wrote credential")
			}
		})
	}
}
func TestBRTST004007DependencyFailuresNeverPersistOrReturnCredentials(t *testing.T) {
	sensitive := errors.New("secret-marker")
	for _, tc := range []struct {
		name   string
		mutate func(*fixture)
	}{
		{"token", func(f *fixture) { f.tokenErr = sensitive }},
		{"id", func(f *fixture) { f.idErr = sensitive }},
		{"invalid_id", func(f *fixture) { f.id = "bad" }},
		{"clock", func(f *fixture) { f.at = time.Time{} }},
		{"url", func(f *fixture) { f.urlErr = sensitive }},
		{"empty_url", func(f *fixture) { f.url = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, true)
			tc.mutate(f)
			r, err := run(f, ptr(first))
			if r != nil || !errors.Is(err, domain.ErrInternal) || f.stored != nil || strings.Contains(err.Error(), "secret-marker") || errors.Is(err, sensitive) {
				t.Fatal("failure exposed or persisted credential")
			}
			for _, event := range f.events {
				if event == "write" {
					t.Fatal("persisted after failure")
				}
			}
		})
	}
}
func TestBRTST001006007FinalAuthorityFailuresReturnNoJoinURL(t *testing.T) {
	for _, pair := range []struct{ source, want error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired}, {port.ErrApplicationTesterJoinLinkAlreadyExists, domain.ErrApplicationTesterJoinLinkAlreadyExists}, {port.ErrApplicationTesterJoinLinkNotFound, domain.ErrApplicationTesterJoinLinkNotFound}, {port.ErrApplicationTesterJoinLinkChanged, domain.ErrApplicationTesterJoinLinkChanged}, {errors.New("tokenHash secret-marker"), domain.ErrInternal},
	} {
		t.Run(pair.source.Error(), func(t *testing.T) {
			f := setup(t, true)
			f.writeErr = pair.source
			r, err := run(f, ptr(first))
			if r != nil || !errors.Is(err, pair.want) || strings.Contains(err.Error(), "secret-marker") || f.stored != nil {
				t.Fatal("final failure leaked or saved credential")
			}
			f = setup(t, true)
			f.loadErr = pair.source
			r, err = run(f, ptr(first))
			if r != nil || !errors.Is(err, pair.want) || !reflect.DeepEqual(f.events, []string{"load"}) {
				t.Fatal("preflight failure generated credential")
			}
		})
	}
}
func TestBRTST005NeverTreatMatchingExpectedAsNoOp(t *testing.T) {
	f := setup(t, true)
	r, err := run(f, ptr(first))
	if err != nil || r.JoinLink().JoinLinkID() == first || f.stored == nil {
		t.Fatal("matching expected became no-op")
	}
}
