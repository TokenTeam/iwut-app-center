package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

type membershipFixture struct {
	events                     []string
	resolved                   *domain.TesterJoinCandidate
	resolveErr, joinErr, idErr error
	id                         string
	at                         time.Time
	hashedRaw                  [32]byte
	resolvedHash, joinedHash   [32]byte
	stored                     *domain.ApplicationTesterMembership
	existing                   *domain.ApplicationTesterMembership
	limit                      int32
}

func (f *membershipFixture) Hash(raw [32]byte) [32]byte {
	f.events = append(f.events, "hash")
	f.hashedRaw = raw
	return sha256.Sum256(raw[:])
}
func (f *membershipFixture) NewUUIDv7() (string, error) {
	f.events = append(f.events, "id")
	return f.id, f.idErr
}
func (f *membershipFixture) Now() time.Time { f.events = append(f.events, "clock"); return f.at }
func (f *membershipFixture) ResolveJoinCandidate(_ context.Context, _ domain.ApplicationTesterJoinLinkID, hash [32]byte) (*domain.TesterJoinCandidate, error) {
	f.events = append(f.events, "resolve")
	f.resolvedHash = hash
	return f.resolved, f.resolveErr
}
func (f *membershipFixture) Join(_ context.Context, link domain.ApplicationTesterJoinLinkID, hash [32]byte, user shared.AuthID, m *domain.ApplicationTesterMembership, limit int32) (*domain.JoinApplicationAsTesterResult, error) {
	f.events = append(f.events, "join")
	f.joinedHash = hash
	f.limit = limit
	if f.joinErr != nil {
		return nil, f.joinErr
	}
	if user != m.TesterAuthID() || link != m.JoinedViaJoinLinkID() {
		return nil, errors.New("wrong membership identity")
	}
	if f.existing != nil {
		return domain.NewJoinApplicationAsTesterResult(f.existing, false, 100, limit)
	}
	f.stored = m
	return domain.NewJoinApplicationAsTesterResult(m, true, 1, limit)
}
func newMembershipFixture() *membershipFixture {
	c, _ := domain.NewTesterJoinCandidate(app)
	return &membershipFixture{resolved: c, id: "01900000-0000-7000-8000-000000000007", at: now}
}
func membershipSecret() string {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
func runMembership(f *membershipFixture) (*domain.JoinApplicationAsTesterResult, error) {
	return NewJoinApplicationAsTesterHandler(f, f, f, f).Handle(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "ordinary-user"}, first, JoinApplicationAsTesterCommand{Secret: membershipSecret()})
}

func TestBRTST010011012016018OrdinaryUserJoinHashesDecodedBytes(t *testing.T) {
	f := newMembershipFixture()
	result, err := runMembership(f)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Joined() || f.stored.TesterAuthID() != "ordinary-user" || f.stored.ApplicationID() != app || f.stored.JoinedViaJoinLinkID() != first || !f.stored.JoinedAt().Equal(now) || f.limit != 100 {
		t.Fatal("incorrect join")
	}
	if !reflect.DeepEqual(f.events, []string{"hash", "resolve", "id", "clock", "join"}) {
		t.Fatal(f.events)
	}
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	if f.hashedRaw != raw || f.resolvedHash != sha256.Sum256(raw[:]) || f.joinedHash != f.resolvedHash || f.joinedHash == sha256.Sum256([]byte(membershipSecret())) {
		t.Fatal("hash must cover raw bytes")
	}
}
func TestBRTST010011InvalidJoinInputsHaveNoEffects(t *testing.T) {
	valid := membershipSecret()
	for _, tc := range []struct {
		name   string
		who    shared.AuthenticatedUserIdentity
		link   domain.ApplicationTesterJoinLinkID
		secret string
		want   error
	}{
		{"missing_user", shared.AuthenticatedUserIdentity{}, first, valid, domain.ErrAuthenticatedUserRequired},
		{"invalid_link", shared.AuthenticatedUserIdentity{AuthID: "u"}, "bad", valid, domain.ErrInvalidTesterJoinLinkId},
		{"empty", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, "", domain.ErrInvalidTesterJoinSecret},
		{"padding", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, valid + "=", domain.ErrInvalidTesterJoinSecret},
		{"short", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, base64.RawURLEncoding.EncodeToString(make([]byte, 31)), domain.ErrInvalidTesterJoinSecret},
		{"long", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, base64.RawURLEncoding.EncodeToString(make([]byte, 33)), domain.ErrInvalidTesterJoinSecret},
		{"newline", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, valid[:20] + "\n" + valid[20:], domain.ErrInvalidTesterJoinSecret},
		{"carriage_return", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, valid[:20] + "\r" + valid[20:], domain.ErrInvalidTesterJoinSecret},
		{"noncanonical_bits", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, strings.Repeat("A", 42) + "B", domain.ErrInvalidTesterJoinSecret},
		{"standard_alphabet", shared.AuthenticatedUserIdentity{AuthID: "u"}, first, strings.Repeat("/", 43), domain.ErrInvalidTesterJoinSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMembershipFixture()
			r, err := NewJoinApplicationAsTesterHandler(f, f, f, f).Handle(context.Background(), tc.who, tc.link, JoinApplicationAsTesterCommand{Secret: tc.secret})
			if r != nil || !errors.Is(err, tc.want) || len(f.events) > 0 {
				t.Fatalf("effects=%v err=%v", f.events, err)
			}
		})
	}
}
func TestBRTST013014IdempotentFullMembershipRetainsOriginalAudit(t *testing.T) {
	f := newMembershipFixture()
	oldAt := now.Add(-time.Hour)
	old, _ := domain.NewActiveTesterMembership("01900000-0000-7000-8000-000000000009", app, "ordinary-user", second, oldAt)
	f.existing = old
	r, err := runMembership(f)
	if err != nil || r.Joined() || r.ActiveTesterCount() != 100 || r.Membership().MembershipID() != old.MembershipID() || !r.Membership().JoinedAt().Equal(oldAt) || r.Membership().JoinedViaJoinLinkID() != second || f.stored != nil {
		t.Fatalf("idempotence lost original episode: %v", err)
	}
}
func TestBRTST011014017019JoinFailuresNoPartialResultOrSensitiveCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*membershipFixture)
		want   error
	}{
		{"invalid_preflight", func(f *membershipFixture) { f.resolveErr = port.ErrTesterJoinLinkInvalid }, domain.ErrTesterJoinLinkInvalid},
		{"invalid_final", func(f *membershipFixture) { f.joinErr = port.ErrTesterJoinLinkInvalid }, domain.ErrTesterJoinLinkInvalid},
		{"full", func(f *membershipFixture) { f.joinErr = port.ErrApplicationTesterLimitReached }, domain.ErrApplicationTesterLimitReached},
		{"nil_candidate", func(f *membershipFixture) { f.resolved = nil }, domain.ErrInternal},
		{"id_error", func(f *membershipFixture) { f.idErr = errors.New("secret-sensitive") }, domain.ErrInternal},
		{"invalid_id", func(f *membershipFixture) { f.id = "bad" }, domain.ErrInternal},
		{"zero_clock", func(f *membershipFixture) { f.at = time.Time{} }, domain.ErrInternal},
		{"storage", func(f *membershipFixture) { f.joinErr = errors.New("secret-sensitive") }, domain.ErrInternal},
		{"preflight_storage", func(f *membershipFixture) { f.resolveErr = errors.New("secret-sensitive") }, domain.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMembershipFixture()
			tc.mutate(f)
			r, err := runMembership(f)
			if r != nil || !errors.Is(err, tc.want) || f.stored != nil || strings.Contains(fmt.Sprintf("%+v", err), "secret-sensitive") || errors.Unwrap(err) != nil {
				t.Fatalf("bad failure: %v", err)
			}
		})
	}
}
func TestBRTST019CommandDiagnosticsRedactSecret(t *testing.T) {
	c := JoinApplicationAsTesterCommand{Secret: membershipSecret()}
	for _, v := range []any{c, &c} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			s := fmt.Sprintf(format, v)
			if strings.Contains(s, c.Secret) || !strings.Contains(s, "redacted") {
				t.Fatal("command exposed secret")
			}
		}
	}
}
