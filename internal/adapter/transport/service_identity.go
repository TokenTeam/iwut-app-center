package transport

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"

	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
)

const (
	ServiceAuthorizationHeader    = "authorization"
	PermissionOAuthClientRead     = "app.oauth.client.read"
	PermissionOAuthClientVerify   = "app.oauth.client.verify"
	PermissionOAuthRuntimeResolve = "app.oauth.runtime.resolve"
	PermissionOAuthContextResolve = "app.oauth.context.resolve"
	PermissionOAuthRedirectsRead  = "app.oauth.redirects.read"
	serviceIdentityAudience       = "iwut-app-center"
)

var (
	errServiceIdentityRequired = errors.New("service identity is required")
	errServiceIdentityInvalid  = errors.New("service identity is invalid")
	errServicePermissionDenied = errors.New("service permission is denied")
)

type ServiceIdentity struct {
	ServiceID   string
	permissions map[string]struct{}
}

func (identity ServiceIdentity) HasPermission(permission string) bool {
	_, ok := identity.permissions[permission]
	return ok
}

type serviceCaller struct {
	active      bool
	keys        map[string]*rsa.PublicKey
	permissions map[string]struct{}
}

type ServiceCallerConfig struct {
	ServiceID         string
	Status            string
	PublicKeyPEMByKID map[string][]byte
	Permissions       []string
}

type ServiceIdentityConfig struct {
	Callers   []ServiceCallerConfig
	MaxTTL    time.Duration
	ClockSkew time.Duration
}

type ServiceIdentityVerifier struct {
	callers           map[string]serviceCaller
	maxTTL, clockSkew time.Duration
	clock             Clock
}

func NewServiceIdentityVerifier(configuration ServiceIdentityConfig, clock Clock) (*ServiceIdentityVerifier, error) {
	if len(configuration.Callers) == 0 || configuration.MaxTTL <= 0 || configuration.ClockSkew < 0 || clock == nil {
		return nil, errors.New("service identity verifier: invalid configuration")
	}
	callers := make(map[string]serviceCaller, len(configuration.Callers))
	for _, registration := range configuration.Callers {
		if registration.ServiceID == "" || strings.TrimSpace(registration.ServiceID) != registration.ServiceID || (registration.Status != "ACTIVE" && registration.Status != "DISABLED") || len(registration.PublicKeyPEMByKID) == 0 {
			return nil, errors.New("service identity verifier: invalid caller registration")
		}
		if _, exists := callers[registration.ServiceID]; exists {
			return nil, fmt.Errorf("service identity verifier: duplicate service %q", registration.ServiceID)
		}
		keys := make(map[string]*rsa.PublicKey, len(registration.PublicKeyPEMByKID))
		for kid, pem := range registration.PublicKeyPEMByKID {
			if kid == "" || strings.TrimSpace(kid) != kid {
				return nil, fmt.Errorf("service identity verifier: invalid key ID for %q", registration.ServiceID)
			}
			key, err := parseRSAPublicKeyPEM(pem)
			if err != nil || key.N == nil || key.N.BitLen() < minimumRSAKeyBits {
				return nil, fmt.Errorf("service identity verifier: invalid key %q for %q", kid, registration.ServiceID)
			}
			keys[kid] = key
		}
		permissions := make(map[string]struct{}, len(registration.Permissions))
		for _, permission := range registration.Permissions {
			if _, duplicate := permissions[permission]; duplicate {
				return nil, fmt.Errorf("service identity verifier: duplicate permission %q", permission)
			}
			switch permission {
			case PermissionOAuthClientRead, PermissionOAuthClientVerify, PermissionOAuthRuntimeResolve, PermissionOAuthContextResolve, PermissionOAuthRedirectsRead:
				permissions[permission] = struct{}{}
			default:
				return nil, fmt.Errorf("service identity verifier: unknown permission %q", permission)
			}
		}
		callers[registration.ServiceID] = serviceCaller{registration.Status == "ACTIVE", keys, permissions}
	}
	return &ServiceIdentityVerifier{callers, configuration.MaxTTL, configuration.ClockSkew, clock}, nil
}

type serviceJOSEHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}
type serviceClaims struct {
	Iss *string         `json:"iss"`
	Sub *string         `json:"sub"`
	Aud json.RawMessage `json:"aud"`
	Iat *float64        `json:"iat"`
	Nbf *float64        `json:"nbf"`
	Exp *float64        `json:"exp"`
	Jti *string         `json:"jti"`
}

func (verifier *ServiceIdentityVerifier) Verify(token string) (ServiceIdentity, error) {
	if verifier == nil || token == "" || token != strings.TrimSpace(token) {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	var header serviceJOSEHeader
	if strictServiceJSON(headerBytes, &header) != nil || header.Alg != "RS256" || header.Typ != "JWT" || header.Kid == "" {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	var claims serviceClaims
	if json.Unmarshal(payloadBytes, &claims) != nil || claims.Iss == nil || claims.Sub == nil || claims.Iat == nil || claims.Nbf == nil || claims.Exp == nil || claims.Jti == nil || *claims.Iss == "" || *claims.Sub != *claims.Iss || *claims.Jti == "" || math.Trunc(*claims.Iat) != *claims.Iat || math.Trunc(*claims.Nbf) != *claims.Nbf || math.Trunc(*claims.Exp) != *claims.Exp {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	caller, ok := verifier.callers[*claims.Iss]
	if !ok || !caller.active {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	key := caller.keys[header.Kid]
	if key == nil {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	if !audienceContains(claims.Aud, serviceIdentityAudience) || *claims.Exp <= *claims.Iat || *claims.Exp <= *claims.Nbf || *claims.Exp-*claims.Iat > verifier.maxTTL.Seconds() {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	now := float64(verifier.clock.Now().UTC().Unix())
	skew := verifier.clockSkew.Seconds()
	if now > *claims.Exp+skew || *claims.Nbf-skew > now || *claims.Iat > now+skew {
		return ServiceIdentity{}, errServiceIdentityInvalid
	}
	return ServiceIdentity{*claims.Sub, caller.permissions}, nil
}

func strictServiceJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func providerPermission(operation string) (string, bool) {
	switch operation {
	case oauthclientv1.OAuthClientProviderService_GetClientConfiguration_FullMethodName:
		return PermissionOAuthClientRead, true
	case oauthclientv1.OAuthClientProviderService_VerifyClientSecret_FullMethodName:
		return PermissionOAuthClientVerify, true
	case oauthclientv1.OAuthClientProviderService_ResolveClientRuntimeConfiguration_FullMethodName:
		return PermissionOAuthRuntimeResolve, true
	case oauthclientv1.OAuthClientProviderService_ResolveAuthorizationContext_FullMethodName:
		return PermissionOAuthContextResolve, true
	case oauthclientv1.OAuthClientProviderService_GetApplicationPublishedRedirects_FullMethodName:
		return PermissionOAuthRedirectsRead, true
	default:
		return "", false
	}
}

func authenticationMiddleware(user *IdentityVerifier, service *ServiceIdentityVerifier) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		userHandler := identityMiddleware(user)(handler)
		return func(ctx context.Context, request any) (any, error) {
			transporter, ok := kratostransport.FromServerContext(ctx)
			if !ok {
				return userHandler(ctx, request)
			}
			permission, provider := providerPermission(transporter.Operation())
			if !provider {
				return userHandler(ctx, request)
			}
			values := transporter.RequestHeader().Values(ServiceAuthorizationHeader)
			if len(values) == 0 || len(values) == 1 && strings.TrimSpace(values[0]) == "" {
				return nil, serviceIdentityTransportError(errServiceIdentityRequired)
			}
			if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
				return nil, serviceIdentityTransportError(errServiceIdentityInvalid)
			}
			token := strings.TrimPrefix(values[0], "Bearer ")
			identity, err := service.Verify(token)
			if err != nil {
				return nil, serviceIdentityTransportError(err)
			}
			if !identity.HasPermission(permission) {
				return nil, serviceIdentityTransportError(errServicePermissionDenied)
			}
			return handler(ctx, request)
		}
	}
}

func serviceIdentityTransportError(err error) error {
	if errors.Is(err, errServiceIdentityRequired) {
		return transportStatus(codes.Unauthenticated, ReasonServiceIdentityRequired, "service identity is required")
	}
	if errors.Is(err, errServicePermissionDenied) {
		return transportStatus(codes.PermissionDenied, ReasonOAuthProviderPermissionDenied, "service is not allowed to call this operation")
	}
	return transportStatus(codes.Unauthenticated, ReasonInvalidServiceIdentity, "service identity is invalid")
}
