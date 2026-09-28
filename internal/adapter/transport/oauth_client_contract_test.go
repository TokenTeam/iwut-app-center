package transport

import (
	"strings"
	"testing"

	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestAPIContract_UCAPP018_RoutesAndMethods(t *testing.T) {
	service := oauthclientv1.File_app_center_v1_oauth_client_oauth_client_proto.Services().ByName("OAuthClientService")
	for _, test := range []struct {
		name, verb, internal, external, body, operation, fullMethod string
	}{
		{"RegisterOAuthClient", "POST", RegisterOAuthClientInternalPath, RegisterOAuthClientExternalPath, "command", RegisterOAuthClientGRPCMethod, oauthclientv1.OAuthClientService_RegisterOAuthClient_FullMethodName},
		{"GetApplicationOAuthRegistration", "GET", GetApplicationOAuthRegistrationInternalPath, GetApplicationOAuthRegistrationExternalPath, "", GetApplicationOAuthRegistrationGRPCMethod, oauthclientv1.OAuthClientService_GetApplicationOAuthRegistration_FullMethodName},
		{"SetOAuthClientStatus", "PUT", SetOAuthClientStatusInternalPath, SetOAuthClientStatusExternalPath, "command", SetOAuthClientStatusGRPCMethod, oauthclientv1.OAuthClientService_SetOAuthClientStatus_FullMethodName},
		{"GetOAuthClientCredentialMetadata", "GET", GetOAuthClientCredentialMetadataInternalPath, GetOAuthClientCredentialMetadataExternalPath, "", GetOAuthClientCredentialMetadataGRPCMethod, oauthclientv1.OAuthClientService_GetOAuthClientCredentialMetadata_FullMethodName},
		{"RotateOAuthClientSecret", "POST", RotateOAuthClientSecretInternalPath, RotateOAuthClientSecretExternalPath, "command", RotateOAuthClientSecretGRPCMethod, oauthclientv1.OAuthClientService_RotateOAuthClientSecret_FullMethodName},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := service.Methods().ByName(protoreflectName(test.name))
			if method == nil {
				t.Fatal("method descriptor missing")
			}
			rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
			var path string
			switch test.verb {
			case "POST":
				path = rule.GetPost()
			case "PUT":
				path = rule.GetPut()
			case "GET":
				path = rule.GetGet()
			}
			if path != test.internal || rule.GetBody() != test.body || test.external != ServicePrefix+test.internal || strings.TrimPrefix(test.external, ServicePrefix) != test.internal || test.operation != test.fullMethod {
				t.Fatalf("contract drift rule=%v operation=%q full=%q", rule, test.operation, test.fullMethod)
			}
		})
	}
}

func protoreflectName(value string) protoreflect.Name { return protoreflect.Name(value) }

func TestAPIContract_UCAPP018_FieldsExcludeSecretsAndPersistenceDetails(t *testing.T) {
	for _, test := range []struct {
		message proto.Message
		fields  string
	}{
		{&oauthclientv1.RegisterOAuthClientRequest{}, "application_id,channel,command"},
		{&oauthclientv1.RegisterOAuthClientCommand{}, "expected_registration_revision,type"},
		{&oauthclientv1.ApplicationOAuthRegistrationResource{}, "application_id,channel,confidential_client,created_at,public_client,registration_revision,updated_at"},
		{&oauthclientv1.OAuthClientIdentityResource{}, "authorization_epoch,client_id,created_at,created_by,status,status_updated_at,status_updated_by,type"},
		{&oauthclientv1.OAuthClientCredentialMetadata{}, "client_id,credential_revision,rotated_at,rotated_by"},
	} {
		if fields := strings.Join(messageFieldNames(t, test.message), ","); fields != test.fields {
			t.Fatalf("field boundary drift for %T: %s", test.message, fields)
		}
	}
	for _, message := range []proto.Message{
		&oauthclientv1.ApplicationOAuthRegistrationResource{},
		&oauthclientv1.OAuthClientIdentityResource{},
		&oauthclientv1.OAuthClientCredentialMetadata{},
	} {
		for _, forbidden := range []string{"client_secret", "secret_digest", "coordination_revision", "admin_id"} {
			if containsField(message, forbidden) {
				t.Fatalf("%T exposes %s", message, forbidden)
			}
		}
	}
}

func TestAPIContract_UCAPP018_ErrorReasonsMatchGeneratedEnum(t *testing.T) {
	for _, spec := range oauthClientDomainErrorSpecs {
		if _, ok := oauthclientv1.ErrorReason_value[spec.reason]; !ok {
			t.Fatalf("missing error reason %s", spec.reason)
		}
	}
	if _, ok := oauthclientv1.ErrorReason_value[ReasonInvalidDeveloperIdentity]; !ok {
		t.Fatal("missing invalid developer identity reason")
	}
}
