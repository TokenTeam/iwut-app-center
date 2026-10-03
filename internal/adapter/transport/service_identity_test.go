package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func newServiceVerifier(t *testing.T, status string, permissions []string) (*ServiceIdentityVerifier, *rsa.PrivateKey) {
	t.Helper()
	privateKey, publicKey, _ := testKeys()
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewServiceIdentityVerifier(ServiceIdentityConfig{
		Callers: []ServiceCallerConfig{{
			ServiceID: "iwut-auth-center", Status: status,
			PublicKeyPEMByKID: map[string][]byte{"primary": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})},
			Permissions:       permissions,
		}},
		MaxTTL: time.Minute, ClockSkew: 30 * time.Second,
	}, fixedClock{now: fixedNow()})
	if err != nil {
		t.Fatalf("NewServiceIdentityVerifier() error = %v", err)
	}
	return verifier, privateKey
}

func validServiceClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": "iwut-auth-center", "sub": "iwut-auth-center", "aud": []string{"other", serviceIdentityAudience},
		"iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(time.Minute).Unix(), "jti": "service-call-1",
	}
}

func TestServiceIdentityVerifier_VerifiesLocalRegistryAndClaims(t *testing.T) {
	permissions := []string{PermissionOAuthClientRead, PermissionOAuthClientVerify, PermissionOAuthRuntimeResolve, PermissionOAuthContextResolve, PermissionOAuthRedirectsRead}
	verifier, key := newServiceVerifier(t, "ACTIVE", permissions)
	identity, err := verifier.Verify(signToken(t, tokenOptions{signingKey: key, claims: validServiceClaims(fixedNow())}))
	if err != nil || identity.ServiceID != "iwut-auth-center" {
		t.Fatalf("Verify(valid) = (%#v, %v)", identity, err)
	}
	for _, permission := range permissions {
		if !identity.HasPermission(permission) {
			t.Fatalf("valid identity lacks %q", permission)
		}
	}
}

func TestServiceIdentityVerifier_FailsClosed(t *testing.T) {
	verifier, key := newServiceVerifier(t, "ACTIVE", []string{PermissionOAuthClientRead})
	now := fixedNow()
	mutate := func(change func(map[string]any)) string {
		claims := validServiceClaims(now)
		change(claims)
		return signToken(t, tokenOptions{signingKey: key, claims: claims})
	}
	tests := map[string]string{
		"wrong issuer":     mutate(func(c map[string]any) { c["iss"], c["sub"] = "unknown", "unknown" }),
		"subject mismatch": mutate(func(c map[string]any) { c["sub"] = "another" }),
		"wrong audience":   mutate(func(c map[string]any) { c["aud"] = "iwut-auth-center" }),
		"excessive ttl":    mutate(func(c map[string]any) { c["exp"] = now.Add(time.Minute + time.Second).Unix() }),
		"expired beyond skew": mutate(func(c map[string]any) {
			c["iat"], c["nbf"], c["exp"] = now.Add(-2*time.Minute).Unix(), now.Add(-2*time.Minute).Unix(), now.Add(-31*time.Second).Unix()
		}),
		"future issued": mutate(func(c map[string]any) {
			c["iat"] = now.Add(31 * time.Second).Unix()
			c["exp"] = now.Add(time.Minute + 31*time.Second).Unix()
		}),
		"fractional timestamp": mutate(func(c map[string]any) { c["iat"] = float64(now.Unix()) + .5 }),
		"missing jti":          mutate(func(c map[string]any) { delete(c, "jti") }),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(token); !errors.Is(err, errServiceIdentityInvalid) {
				t.Fatalf("Verify() error = %v, want invalid", err)
			}
		})
	}
	t.Run("token supplied key material", func(t *testing.T) {
		token := signToken(t, tokenOptions{signingKey: key, claims: validServiceClaims(now), headerOverride: map[string]any{"jwk": map[string]any{"kty": "RSA"}}})
		if _, err := verifier.Verify(token); !errors.Is(err, errServiceIdentityInvalid) {
			t.Fatalf("Verify() error = %v, want invalid", err)
		}
	})
	disabled, _ := newServiceVerifier(t, "DISABLED", []string{PermissionOAuthClientRead})
	if _, err := disabled.Verify(signToken(t, tokenOptions{signingKey: key, claims: validServiceClaims(now)})); !errors.Is(err, errServiceIdentityInvalid) {
		t.Fatalf("disabled caller error = %v", err)
	}
}

func TestProviderPermissionMappingIsExact(t *testing.T) {
	operations := map[string]string{
		"/app_center.v1.oauth_client.OAuthClientProviderService/GetClientConfiguration":            PermissionOAuthClientRead,
		"/app_center.v1.oauth_client.OAuthClientProviderService/VerifyClientSecret":                PermissionOAuthClientVerify,
		"/app_center.v1.oauth_client.OAuthClientProviderService/ResolveClientRuntimeConfiguration": PermissionOAuthRuntimeResolve,
		"/app_center.v1.oauth_client.OAuthClientProviderService/ResolveAuthorizationContext":       PermissionOAuthContextResolve,
		"/app_center.v1.oauth_client.OAuthClientProviderService/GetApplicationPublishedRedirects":  PermissionOAuthRedirectsRead,
	}
	for operation, want := range operations {
		got, ok := providerPermission(operation)
		if !ok || got != want {
			t.Fatalf("providerPermission(%q) = %q/%t, want %q", operation, got, ok, want)
		}
	}
	if permission, ok := providerPermission("/app_center.v1.oauth_client.OAuthClientService/RegisterOAuthClient"); ok || permission != "" {
		t.Fatalf("management method treated as provider: %q/%t", permission, ok)
	}
}

func TestServiceIdentityVerifierRejectsUnknownPermissionAndWeakKeys(t *testing.T) {
	_, publicKey, _ := testKeys()
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	base := ServiceCallerConfig{ServiceID: "iwut-auth-center", Status: "ACTIVE", PublicKeyPEMByKID: map[string][]byte{"primary": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})}}
	unknown := base
	unknown.Permissions = []string{"app.oauth.unknown"}
	if _, err := NewServiceIdentityVerifier(ServiceIdentityConfig{Callers: []ServiceCallerConfig{unknown}, MaxTTL: time.Minute}, fixedClock{now: fixedNow()}); err == nil {
		t.Fatal("unknown permission accepted")
	}
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	weakDER, err := x509.MarshalPKIXPublicKey(&weakKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	weak := base
	weak.PublicKeyPEMByKID = map[string][]byte{"weak": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: weakDER})}
	if _, err := NewServiceIdentityVerifier(ServiceIdentityConfig{Callers: []ServiceCallerConfig{weak}, MaxTTL: time.Minute}, fixedClock{now: fixedNow()}); err == nil {
		t.Fatal("weak RSA key accepted")
	}
}
