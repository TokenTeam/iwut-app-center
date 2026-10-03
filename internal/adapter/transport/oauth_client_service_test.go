package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	oauthclientdomain "iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

const oauthTransportClientID = "123e4567-e89b-42d3-a456-426614174000"

type fakeOAuthClientHandlers struct {
	registerResult *oauthclientdomain.RegisterResult
	registration   *oauthclientdomain.Registration
	statusResult   *oauthclientdomain.StatusResult
	credential     *oauthclientdomain.Credential
	secret         string
	err            error
	registerCalls  int
	lastType       oauthclientdomain.ClientType
	lastExpected   *int64
}

func (f *fakeOAuthClientHandlers) Register(_ context.Context, _ shared.DeveloperIdentity, _ shared.ApplicationID, _ oauthclientdomain.Channel, typ oauthclientdomain.ClientType, expected *int64) (*oauthclientdomain.RegisterResult, string, error) {
	f.registerCalls++
	f.lastType = typ
	f.lastExpected = expected
	return f.registerResult, f.secret, f.err
}
func (f *fakeOAuthClientHandlers) GetRegistration(context.Context, shared.DeveloperIdentity, shared.ApplicationID, oauthclientdomain.Channel) (*oauthclientdomain.Registration, error) {
	return f.registration, f.err
}
func (f *fakeOAuthClientHandlers) SetStatus(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID, int64, oauthclientdomain.ClientStatus) (*oauthclientdomain.StatusResult, error) {
	return f.statusResult, f.err
}
func (f *fakeOAuthClientHandlers) GetCredential(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID) (*oauthclientdomain.Credential, error) {
	return f.credential, f.err
}
func (f *fakeOAuthClientHandlers) RotateSecret(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID, int64) (*oauthclientdomain.Credential, string, error) {
	return f.credential, f.secret, f.err
}

func oauthTransportFixture(t *testing.T, typ oauthclientdomain.ClientType) (*oauthclientdomain.Registration, *oauthclientdomain.Credential) {
	t.Helper()
	applicationID, ok := shared.ParseApplicationID(testApplicationID)
	if !ok {
		t.Fatal("invalid application fixture")
	}
	clientID, err := oauthclientdomain.ParseClientID(oauthTransportClientID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	identity, err := oauthclientdomain.NewClientIdentity(clientID, typ, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	var public, confidential *oauthclientdomain.ClientIdentity
	if typ == oauthclientdomain.ClientTypePublicPKCE {
		public = identity
	} else {
		confidential = identity
	}
	registration, err := oauthclientdomain.RestoreRegistration(applicationID, oauthclientdomain.ChannelTest, public, confidential, 1, at, at)
	if err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	digest[0] = 5
	credential, err := oauthclientdomain.RestoreCredential(clientID, applicationID, oauthclientdomain.NewSecretDigest(digest), 1, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	return registration, credential
}

func oauthHTTPServers(t *testing.T, handler OAuthClientHandlers) *Servers {
	t.Helper()
	servers, err := NewServers(
		ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil),
		NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil),
		NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil),
		NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(handler),
	)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestOAuthClientServiceRegisterConfidentialResponse(t *testing.T) {
	registration, credential := oauthTransportFixture(t, oauthclientdomain.ClientTypeConfidentialSecret)
	handler := &fakeOAuthClientHandlers{
		registerResult: &oauthclientdomain.RegisterResult{Registration: registration, Credential: credential},
		secret:         "one-time-oauth-secret",
	}
	service := NewOAuthClientService(handler)
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved})
	revision := int64(7)
	response, err := service.RegisterOAuthClient(ctx, &oauthclientv1.RegisterOAuthClientRequest{
		ApplicationId: testApplicationID,
		Channel:       oauthclientv1.OAuthChannel_OAUTH_CHANNEL_TEST,
		Command: &oauthclientv1.RegisterOAuthClientCommand{
			Type:                         oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET,
			ExpectedRegistrationRevision: &revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if handler.registerCalls != 1 || handler.lastType != oauthclientdomain.ClientTypeConfidentialSecret || handler.lastExpected == nil || *handler.lastExpected != revision {
		t.Fatalf("handler input = %#v", handler)
	}
	if response.GetClientSecret() != "one-time-oauth-secret" || response.GetRegistration().GetConfidentialClient().GetClientId() != oauthTransportClientID || response.GetRegistration().GetPublicClient() != nil {
		t.Fatalf("response = %v", response)
	}
}

func TestOAuthClientServiceHTTPRegisterUsesCreatedNoStoreAndOneTimeSecret(t *testing.T) {
	registration, credential := oauthTransportFixture(t, oauthclientdomain.ClientTypeConfidentialSecret)
	handler := &fakeOAuthClientHandlers{registerResult: &oauthclientdomain.RegisterResult{Registration: registration, Credential: credential}, secret: "http-one-time-secret"}
	servers := oauthHTTPServers(t, handler)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/oauth-registrations/OAUTH_CHANNEL_TEST/clients", strings.NewReader(`{"type":"OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(IdentityHeader, token)
	recorder := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Body.String(), "http-one-time-secret") || handler.registerCalls != 1 {
		t.Fatalf("status=%d cache=%q calls=%d body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), handler.registerCalls, recorder.Body.String())
	}
}

func TestOAuthClientServiceHTTPRejectsBeforeBindingWithoutCaching(t *testing.T) {
	handler := &fakeOAuthClientHandlers{}
	servers := oauthHTTPServers(t, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/oauth-registrations/OAUTH_CHANNEL_TEST/clients", strings.NewReader(`{"type":`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("Cache-Control") != "no-store" || handler.registerCalls != 0 || !strings.Contains(recorder.Body.String(), ReasonDeveloperIdentityRequired) {
		t.Fatalf("status=%d cache=%q calls=%d body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), handler.registerCalls, recorder.Body.String())
	}
}

func TestOAuthClientServiceErrorMappings(t *testing.T) {
	for _, test := range []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{oauthclientdomain.ErrInvalidApplicationID, codes.InvalidArgument, ReasonInvalidApplicationID},
		{oauthclientdomain.ErrInvalidOAuthChannel, codes.InvalidArgument, ReasonInvalidOAuthChannel},
		{oauthclientdomain.ErrOAuthChannelNotEnabled, codes.FailedPrecondition, ReasonOAuthChannelNotEnabled},
		{oauthclientdomain.ErrInvalidOAuthClientType, codes.InvalidArgument, ReasonInvalidOAuthClientType},
		{oauthclientdomain.ErrInvalidOAuthClientID, codes.InvalidArgument, ReasonInvalidOAuthClientID},
		{oauthclientdomain.ErrInvalidOAuthClientStatus, codes.InvalidArgument, ReasonInvalidOAuthClientStatus},
		{oauthclientdomain.ErrInvalidRegistrationRevision, codes.InvalidArgument, ReasonInvalidOAuthRegistrationRevision},
		{oauthclientdomain.ErrInvalidCredentialRevision, codes.InvalidArgument, ReasonInvalidOAuthCredentialRevision},
		{oauthclientdomain.ErrApplicationNotFound, codes.NotFound, ReasonApplicationNotFound},
		{oauthclientdomain.ErrApplicationAdminRequired, codes.PermissionDenied, ReasonApplicationAdminRequired},
		{oauthclientdomain.ErrOAuthRegistrationNotFound, codes.NotFound, ReasonOAuthRegistrationNotFound},
		{oauthclientdomain.ErrOAuthClientAlreadyExists, codes.AlreadyExists, ReasonOAuthClientAlreadyExists},
		{oauthclientdomain.ErrOAuthClientNotFound, codes.NotFound, ReasonOAuthClientNotFound},
		{oauthclientdomain.ErrOAuthRegistrationChanged, codes.Aborted, ReasonOAuthRegistrationChanged},
		{oauthclientdomain.ErrOAuthCredentialNotFound, codes.NotFound, ReasonOAuthClientCredentialNotFound},
		{oauthclientdomain.ErrOAuthCredentialChanged, codes.Aborted, ReasonOAuthClientCredentialChanged},
		{oauthclientdomain.ErrOAuthClientStateInconsistent, codes.Internal, ReasonOAuthClientStateInconsistent},
		{errors.New("private database details"), codes.Internal, ReasonInternal},
	} {
		t.Run(test.reason, func(t *testing.T) {
			mapped := status.Convert(toTransportError(test.err))
			if mapped.Code() != test.code || errorReason(mapped) != test.reason || strings.Contains(mapped.Message(), "private database") {
				t.Fatalf("mapped = %v", mapped)
			}
		})
	}
}

func TestOAuthClientServiceRejectsFutureEnums(t *testing.T) {
	service := NewOAuthClientService(&fakeOAuthClientHandlers{})
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, channel := range []oauthclientv1.OAuthChannel{oauthclientv1.OAuthChannel_OAUTH_CHANNEL_GREY} {
		_, err := service.GetApplicationOAuthRegistration(ctx, &oauthclientv1.GetApplicationOAuthRegistrationRequest{ApplicationId: testApplicationID, Channel: channel})
		if mapped := status.Convert(err); mapped.Code() != codes.FailedPrecondition || errorReason(mapped) != ReasonOAuthChannelNotEnabled {
			t.Fatalf("channel=%v error=%v", channel, err)
		}
	}
	_, err := service.GetApplicationOAuthRegistration(ctx, &oauthclientv1.GetApplicationOAuthRegistrationRequest{ApplicationId: testApplicationID, Channel: 99})
	if mapped := status.Convert(err); mapped.Code() != codes.InvalidArgument || errorReason(mapped) != ReasonInvalidOAuthChannel {
		t.Fatalf("unknown enum error=%v", err)
	}
}

func TestOAuthClientService_UCAPP020_StableChannelMapping(t *testing.T) {
	channel, err := oauthChannel(oauthclientv1.OAuthChannel_OAUTH_CHANNEL_STABLE)
	if err != nil || channel != oauthclientdomain.ChannelStable {
		t.Fatalf("channel=%q error=%v", channel, err)
	}
	applicationID, _ := shared.ParseApplicationID(testApplicationID)
	clientID, _ := oauthclientdomain.ParseClientID("123e4567-e89b-42d3-a456-426614174000")
	at := fixedNow()
	identity, err := oauthclientdomain.NewClientIdentity(clientID, oauthclientdomain.ClientTypePublicPKCE, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := oauthclientdomain.RestoreRegistration(applicationID, oauthclientdomain.ChannelStable, identity, nil, 1, at, at)
	if err != nil {
		t.Fatal(err)
	}
	if resource := oauthRegistrationResource(registration); resource.GetChannel() != oauthclientv1.OAuthChannel_OAUTH_CHANNEL_STABLE {
		t.Fatalf("resource=%#v", resource)
	}
}
