package usecase

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

const (
	testApplicationID = "01890f47-0000-7000-8000-000000000018"
	testClientID      = "123e4567-e89b-42d3-a456-426614174000"
)

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

type fakeIDs struct {
	id    domain.ClientID
	err   error
	calls int
}

func (f *fakeIDs) NewUUIDv4() (domain.ClientID, error) { f.calls++; return f.id, f.err }

type fakeSecrets struct {
	plain  string
	digest domain.SecretDigest
	err    error
	calls  int
}

func (f *fakeSecrets) NewSecret(domain.ClientID) (string, domain.SecretDigest, error) {
	f.calls++
	return f.plain, f.digest, f.err
}

type fakeClock struct{ at time.Time }

func (f fakeClock) Now() time.Time { return f.at }

type fakeRepository struct {
	registerResult *domain.RegisterResult
	registration   *domain.Registration
	statusResult   *domain.StatusResult
	credential     *domain.Credential
	err            error
	registerCalls  int
	lastDigest     *domain.SecretDigest
}

func (f *fakeRepository) Register(_ context.Context, _ shared.ApplicationID, _ domain.Channel, _ domain.ClientType, _ *int64, _ domain.ClientID, digest *domain.SecretDigest, _ shared.AuthID, _ time.Time) (*domain.RegisterResult, error) {
	f.registerCalls++
	f.lastDigest = digest
	return f.registerResult, f.err
}
func (f *fakeRepository) GetRegistration(context.Context, shared.ApplicationID, domain.Channel, shared.AuthID) (*domain.Registration, error) {
	return f.registration, f.err
}
func (f *fakeRepository) SetStatus(context.Context, domain.ClientID, int64, domain.ClientStatus, shared.AuthID, time.Time) (*domain.StatusResult, error) {
	return f.statusResult, f.err
}
func (f *fakeRepository) GetCredential(context.Context, domain.ClientID, shared.AuthID) (*domain.Credential, error) {
	return f.credential, f.err
}
func (f *fakeRepository) RotateSecret(context.Context, domain.ClientID, int64, domain.SecretDigest, shared.AuthID, time.Time) (*domain.Credential, error) {
	return f.credential, f.err
}

func oauthFixtures(t *testing.T, typ domain.ClientType) (*domain.Registration, *domain.Credential) {
	t.Helper()
	appID, ok := shared.ParseApplicationID(testApplicationID)
	if !ok {
		t.Fatal("invalid test application ID")
	}
	clientID, err := domain.ParseClientID(testClientID)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := domain.NewClientIdentity(clientID, typ, "admin", testNow)
	if err != nil {
		t.Fatal(err)
	}
	var public, confidential *domain.ClientIdentity
	if typ == domain.ClientTypePublicPKCE {
		public = identity
	} else {
		confidential = identity
	}
	registration, err := domain.RestoreRegistration(appID, domain.ChannelTest, public, confidential, 1, testNow, testNow)
	if err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	digest[0] = 9
	credential, err := domain.RestoreCredential(clientID, appID, domain.NewSecretDigest(digest), 1, "admin", testNow)
	if err != nil {
		t.Fatal(err)
	}
	return registration, credential
}

func approvedIdentity() shared.DeveloperIdentity {
	return shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved}
}

func TestRegisterConfidentialDisclosesSecretOnlyAfterCommittedResult(t *testing.T) {
	registration, credential := oauthFixtures(t, domain.ClientTypeConfidentialSecret)
	clientID, _ := domain.ParseClientID(testClientID)
	var digestBytes [32]byte
	digestBytes[4] = 7
	digest := domain.NewSecretDigest(digestBytes)
	ids := &fakeIDs{id: clientID}
	secrets := &fakeSecrets{plain: "one-time-secret", digest: digest}
	repository := &fakeRepository{registerResult: &domain.RegisterResult{Registration: registration, Credential: credential}}
	handler := NewHandlers(ids, secrets, fakeClock{at: testNow}, repository)
	appID, _ := shared.ParseApplicationID(testApplicationID)

	result, secret, err := handler.Register(t.Context(), approvedIdentity(), appID, domain.ChannelTest, domain.ClientTypeConfidentialSecret, nil)
	if err != nil || result == nil || secret != "one-time-secret" || ids.calls != 1 || secrets.calls != 1 || repository.registerCalls != 1 || repository.lastDigest == nil || repository.lastDigest.Bytes() != digest.Bytes() {
		t.Fatalf("result=%v secret=%q err=%v ids=%d secrets=%d repo=%d digest=%v", result, secret, err, ids.calls, secrets.calls, repository.registerCalls, repository.lastDigest)
	}

	repository.err = port.ErrRegistrationChanged
	result, secret, err = handler.Register(t.Context(), approvedIdentity(), appID, domain.ChannelTest, domain.ClientTypeConfidentialSecret, nil)
	if result != nil || secret != "" || !errors.Is(err, domain.ErrOAuthRegistrationChanged) {
		t.Fatalf("failed commit disclosed result=%v secret=%q err=%v", result, secret, err)
	}
}

func TestRegisterPublicNeverGeneratesOrPersistsSecret(t *testing.T) {
	registration, _ := oauthFixtures(t, domain.ClientTypePublicPKCE)
	clientID, _ := domain.ParseClientID(testClientID)
	secrets := &fakeSecrets{plain: "must-not-be-used"}
	repository := &fakeRepository{registerResult: &domain.RegisterResult{Registration: registration}}
	handler := NewHandlers(&fakeIDs{id: clientID}, secrets, fakeClock{at: testNow}, repository)
	appID, _ := shared.ParseApplicationID(testApplicationID)

	result, secret, err := handler.Register(t.Context(), approvedIdentity(), appID, domain.ChannelTest, domain.ClientTypePublicPKCE, nil)
	if err != nil || result == nil || secret != "" || secrets.calls != 0 || repository.lastDigest != nil {
		t.Fatalf("result=%v secret=%q err=%v secretCalls=%d digest=%v", result, secret, err, secrets.calls, repository.lastDigest)
	}
}

func TestRegisterValidatesAuthorizationAndOCCBeforeGeneratingCredentials(t *testing.T) {
	appID, _ := shared.ParseApplicationID(testApplicationID)
	clientID, _ := domain.ParseClientID(testClientID)
	ids := &fakeIDs{id: clientID}
	secrets := &fakeSecrets{plain: "unused"}
	repository := &fakeRepository{}
	handler := NewHandlers(ids, secrets, fakeClock{at: testNow}, repository)
	zero := int64(0)

	for _, test := range []struct {
		name     string
		identity shared.DeveloperIdentity
		channel  domain.Channel
		typ      domain.ClientType
		expected *int64
		want     domain.ErrorCode
	}{
		{"identity required", shared.DeveloperIdentity{}, domain.ChannelTest, domain.ClientTypePublicPKCE, nil, domain.ErrorCodeDeveloperIdentityRequired},
		{"approval required", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, domain.ChannelTest, domain.ClientTypePublicPKCE, nil, domain.ErrorCodeDeveloperApprovalRequired},
		{"channel disabled", approvedIdentity(), domain.Channel("OTHER"), domain.ClientTypePublicPKCE, nil, domain.ErrorCodeOAuthChannelNotEnabled},
		{"type invalid", approvedIdentity(), domain.ChannelTest, "OTHER", nil, domain.ErrorCodeInvalidOAuthClientType},
		{"revision invalid", approvedIdentity(), domain.ChannelTest, domain.ClientTypePublicPKCE, &zero, domain.ErrorCodeInvalidRegistrationRevision},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, secret, err := handler.Register(t.Context(), test.identity, appID, test.channel, test.typ, test.expected)
			if secret != "" || !domain.IsCode(err, test.want) {
				t.Fatalf("secret=%q error=%v", secret, err)
			}
		})
	}
	if ids.calls != 0 || secrets.calls != 0 || repository.registerCalls != 0 {
		t.Fatalf("validation had side effects ids=%d secrets=%d repo=%d", ids.calls, secrets.calls, repository.registerCalls)
	}
}

func TestRotateSecretUsesIndependentCredentialRevision(t *testing.T) {
	_, oldCredential := oauthFixtures(t, domain.ClientTypeConfidentialSecret)
	appID := oldCredential.ApplicationID()
	clientID := oldCredential.ClientID()
	var digestBytes [32]byte
	digestBytes[2] = 3
	newCredential, err := domain.RestoreCredential(clientID, appID, domain.NewSecretDigest(digestBytes), 2, "admin", testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{credential: newCredential}
	secrets := &fakeSecrets{plain: "rotated-secret", digest: domain.NewSecretDigest(digestBytes)}
	handler := NewHandlers(&fakeIDs{}, secrets, fakeClock{at: testNow.Add(time.Minute)}, repository)

	credential, secret, err := handler.RotateSecret(t.Context(), approvedIdentity(), clientID, 1)
	if err != nil || credential == nil || credential.Revision() != 2 || secret != "rotated-secret" {
		t.Fatalf("credential=%v secret=%q err=%v", credential, secret, err)
	}
	if _, secret, err = handler.RotateSecret(t.Context(), approvedIdentity(), clientID, math.MaxInt64); secret != "" || !errors.Is(err, domain.ErrInvalidCredentialRevision) {
		t.Fatalf("overflow secret=%q err=%v", secret, err)
	}
}

func TestRepositoryErrorsMapToStableDomainErrors(t *testing.T) {
	for _, test := range []struct {
		source error
		want   domain.ErrorCode
	}{
		{port.ErrApplicationNotFound, domain.ErrorCodeApplicationNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrorCodeApplicationAdminRequired},
		{port.ErrRegistrationNotFound, domain.ErrorCodeOAuthRegistrationNotFound},
		{port.ErrClientAlreadyExists, domain.ErrorCodeOAuthClientAlreadyExists},
		{port.ErrClientNotFound, domain.ErrorCodeOAuthClientNotFound},
		{port.ErrRegistrationChanged, domain.ErrorCodeOAuthRegistrationChanged},
		{port.ErrCredentialNotFound, domain.ErrorCodeOAuthCredentialNotFound},
		{port.ErrCredentialChanged, domain.ErrorCodeOAuthCredentialChanged},
		{port.ErrStateInconsistent, domain.ErrorCodeOAuthClientStateInconsistent},
		{errors.New("private database failure"), domain.ErrorCodeInternal},
	} {
		if err := mapRepositoryError(test.source); !domain.IsCode(err, test.want) {
			t.Fatalf("source=%v mapped=%v want=%s", test.source, err, test.want)
		}
	}
}
