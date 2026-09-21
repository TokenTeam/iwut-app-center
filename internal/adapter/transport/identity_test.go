package transport

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	testIssuer   = "https://auth.example.test"
	testAudience = "iwut-app-center"
	tokenSubject = "auth-123"
)

var (
	testKeyOnce         sync.Once
	testPrivateKey      *rsa.PrivateKey
	testPublicKey       *rsa.PublicKey
	testOtherPrivateKey *rsa.PrivateKey
)

func testKeys() (*rsa.PrivateKey, *rsa.PublicKey, *rsa.PrivateKey) {
	testKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testPrivateKey = key
		testPublicKey = &key.PublicKey
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testOtherPrivateKey = other
	})
	return testPrivateKey, testPublicKey, testOtherPrivateKey
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

func fixedNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

func newTestVerifier(t *testing.T) *IdentityVerifier {
	t.Helper()
	_, publicKey, _ := testKeys()
	verifier, err := NewIdentityVerifier(IdentityConfig{
		Issuer:     testIssuer,
		Audience:   testAudience,
		MaxTTL:     5 * time.Minute,
		ClockSkew:  30 * time.Second,
		PublicKeys: map[string]*rsa.PublicKey{"primary": publicKey},
		Clock:      fixedClock{now: fixedNow()},
	})
	if err != nil {
		t.Fatalf("NewIdentityVerifier() error = %v", err)
	}
	return verifier
}

func validClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss":              testIssuer,
		"sub":              tokenSubject,
		"aud":              []string{"another-service", testAudience},
		"iat":              now.Unix(),
		"nbf":              now.Add(-time.Minute).Unix(),
		"exp":              now.Add(4 * time.Minute).Unix(),
		"jti":              "token-id-1",
		"developer_status": "APPROVED",
	}
}

type tokenOptions struct {
	alg            string
	typ            string
	kid            string
	signingKey     *rsa.PrivateKey
	claims         map[string]any
	corrupt        bool
	forceSignature string
	headerOverride map[string]any
}

func signToken(t *testing.T, options tokenOptions) string {
	t.Helper()
	testKeys()

	alg := options.alg
	if alg == "" {
		alg = "RS256"
	}
	typ := options.typ
	if typ == "" {
		typ = "JWT"
	}
	kid := options.kid
	if kid == "" {
		kid = "primary"
	}
	signingKey := options.signingKey
	if signingKey == nil {
		signingKey = testPrivateKey
	}

	header := map[string]any{"alg": alg, "typ": typ, "kid": kid}
	for key, value := range options.headerOverride {
		header[key] = value
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(options.claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	var signature []byte
	if options.forceSignature != "" {
		signature, err = base64.RawURLEncoding.DecodeString(options.forceSignature)
		if err != nil {
			t.Fatalf("decode forced signature: %v", err)
		}
	} else {
		digest := sha256.Sum256([]byte(signingInput))
		signature, err = rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
	}
	if options.corrupt && len(signature) > 0 {
		signature[0] ^= 0xff
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestIdentityVerifier_ValidToken(t *testing.T) {
	t.Parallel()

	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	identity, err := newTestVerifier(t).Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.AuthID != shared.AuthID(tokenSubject) {
		t.Fatalf("AuthID = %q, want %q", identity.AuthID, tokenSubject)
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		t.Fatalf("DeveloperStatus = %q, want APPROVED", identity.DeveloperStatus)
	}
}

func TestIdentityVerifier_ValidTokenStringAudience(t *testing.T) {
	t.Parallel()

	claims := validClaims(fixedNow())
	claims["aud"] = testAudience
	token := signToken(t, tokenOptions{claims: claims})
	if _, err := newTestVerifier(t).Verify(token); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestIdentityVerifier_ReviewerTokenWithoutDeveloperStatus(t *testing.T) {
	t.Parallel()

	claims := validClaims(fixedNow())
	delete(claims, "developer_status")
	claims["permissions"] = []string{"app.version.review", "future.permission"}
	identity, err := newTestVerifier(t).Verify(signToken(t, tokenOptions{claims: claims}))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.AuthID != tokenSubject || identity.DeveloperStatus != "" ||
		len(identity.Permissions) != 2 || identity.Permissions[0] != "app.version.review" {
		t.Fatalf("reviewer identity = %#v", identity)
	}
}

func TestIdentityVerifier_RejectsInvalidTokens(t *testing.T) {
	t.Parallel()

	now := fixedNow()

	testCases := []struct {
		name    string
		token   func(t *testing.T) string
		wantErr error
	}{
		{
			name:    "empty",
			token:   func(t *testing.T) string { return "" },
			wantErr: errIdentityRequired,
		},
		{
			name:    "whitespace",
			token:   func(t *testing.T) string { return "   " },
			wantErr: errIdentityRequired,
		},
		{
			name:  "two segments",
			token: func(t *testing.T) string { return "a.b" },
		},
		{
			name: "wrong signature",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), corrupt: true})
			},
		},
		{
			name: "signed by another key",
			token: func(t *testing.T) string {
				_, _, otherKey := testKeys()
				return signToken(t, tokenOptions{claims: validClaims(now), signingKey: otherKey})
			},
		},
		{
			name: "unknown kid",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), kid: "ghost"})
			},
		},
		{
			name: "wrong alg",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), alg: "HS256"})
			},
		},
		{
			name: "none alg",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), alg: "none"})
			},
		},
		{
			name: "wrong typ",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), typ: "JWS"})
			},
		},
		{
			name: "lowercase typ",
			token: func(t *testing.T) string {
				return signToken(t, tokenOptions{claims: validClaims(now), typ: "jwt"})
			},
		},
		{
			name: "missing audience",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				delete(claims, "aud")
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "wrong audience",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["aud"] = []string{"some-other-service"}
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "wrong issuer",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["iss"] = "https://evil.example.test"
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["iat"] = now.Add(-10 * time.Minute).Unix()
				claims["nbf"] = now.Add(-10 * time.Minute).Unix()
				claims["exp"] = now.Add(-5 * time.Minute).Unix()
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "not yet valid",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["nbf"] = now.Add(time.Minute).Unix()
				claims["iat"] = now.Unix()
				claims["exp"] = now.Add(2 * time.Minute).Unix()
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "issued in the future",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["iat"] = now.Add(time.Minute).Unix()
				claims["nbf"] = now.Unix()
				claims["exp"] = now.Add(2 * time.Minute).Unix()
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "ttl exceeds maximum",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["iat"] = now.Unix()
				claims["nbf"] = now.Unix()
				claims["exp"] = now.Add(6 * time.Minute).Unix()
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "exp not after nbf",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["nbf"] = now.Add(2 * time.Minute).Unix()
				claims["exp"] = now.Add(time.Minute).Unix()
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "illegal developer status",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["developer_status"] = "BANNED"
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "duplicate permission",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["permissions"] = []string{"app.version.review", "app.version.review"}
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "permission with surrounding whitespace",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["permissions"] = []string{" app.version.review"}
				return signToken(t, tokenOptions{claims: claims})
			},
		},
		{
			name: "empty subject",
			token: func(t *testing.T) string {
				claims := validClaims(now)
				claims["sub"] = ""
				return signToken(t, tokenOptions{claims: claims})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			wantErr := testCase.wantErr
			if wantErr == nil {
				wantErr = errIdentityInvalid
			}
			if _, err := newTestVerifier(t).Verify(testCase.token(t)); !errors.Is(err, wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestIdentityVerifier_RejectsMissingClaims(t *testing.T) {
	t.Parallel()

	now := fixedNow()
	for _, missing := range []string{"iss", "sub", "aud", "iat", "nbf", "exp", "jti"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			claims := validClaims(now)
			delete(claims, missing)
			token := signToken(t, tokenOptions{claims: claims})
			if _, err := newTestVerifier(t).Verify(token); !errors.Is(err, errIdentityInvalid) {
				t.Fatalf("Verify() error = %v, want errIdentityInvalid", err)
			}
		})
	}
}

func TestNewIdentityVerifier_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, publicKey, _ := testKeys()
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate weak RSA key: %v", err)
	}
	valid := IdentityConfig{
		Issuer:     testIssuer,
		Audience:   testAudience,
		MaxTTL:     5 * time.Minute,
		ClockSkew:  30 * time.Second,
		PublicKeys: map[string]*rsa.PublicKey{"primary": publicKey},
		Clock:      fixedClock{now: fixedNow()},
	}

	testCases := []struct {
		name   string
		mutate func(config *IdentityConfig)
	}{
		{name: "empty issuer", mutate: func(c *IdentityConfig) { c.Issuer = "" }},
		{name: "empty audience", mutate: func(c *IdentityConfig) { c.Audience = "" }},
		{name: "zero max ttl", mutate: func(c *IdentityConfig) { c.MaxTTL = 0 }},
		{name: "negative clock skew", mutate: func(c *IdentityConfig) { c.ClockSkew = -time.Second }},
		{name: "no keys", mutate: func(c *IdentityConfig) { c.PublicKeys = nil }},
		{name: "nil key", mutate: func(c *IdentityConfig) { c.PublicKeys = map[string]*rsa.PublicKey{"primary": nil} }},
		{name: "empty kid", mutate: func(c *IdentityConfig) { c.PublicKeys = map[string]*rsa.PublicKey{" ": publicKey} }},
		{name: "weak key", mutate: func(c *IdentityConfig) { c.PublicKeys = map[string]*rsa.PublicKey{"primary": &weakKey.PublicKey} }},
		{name: "nil clock", mutate: func(c *IdentityConfig) { c.Clock = nil }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			testCase.mutate(&config)
			if _, err := NewIdentityVerifier(config); err == nil {
				t.Fatal("NewIdentityVerifier() error = nil, want error")
			}
		})
	}
}

func TestLoadRSAPublicKeys(t *testing.T) {
	t.Parallel()

	_, publicKey, _ := testKeys()
	pkixDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal PKIX: %v", err)
	}
	directory := t.TempDir()
	pkixPath := filepath.Join(directory, "pkix.pem")
	if err := os.WriteFile(pkixPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkixDER}), 0o600); err != nil {
		t.Fatalf("write pkix pem: %v", err)
	}
	pkcs1Path := filepath.Join(directory, "pkcs1.pem")
	if err := os.WriteFile(pkcs1Path, pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(publicKey)}), 0o600); err != nil {
		t.Fatalf("write pkcs1 pem: %v", err)
	}

	keys, err := LoadRSAPublicKeys(map[string]string{"pkix": pkixPath, "pkcs1": pkcs1Path})
	if err != nil {
		t.Fatalf("LoadRSAPublicKeys() error = %v", err)
	}
	if len(keys) != 2 || keys["pkix"] == nil || keys["pkcs1"] == nil {
		t.Fatalf("LoadRSAPublicKeys() = %#v", keys)
	}

	invalidPath := filepath.Join(directory, "bad.pem")
	if err := os.WriteFile(invalidPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write invalid pem: %v", err)
	}
	if _, err := LoadRSAPublicKeys(map[string]string{"bad": invalidPath}); err == nil {
		t.Fatal("LoadRSAPublicKeys(invalid) error = nil, want error")
	}
	if _, err := LoadRSAPublicKeys(map[string]string{"missing": filepath.Join(directory, "nope.pem")}); err == nil {
		t.Fatal("LoadRSAPublicKeys(missing) error = nil, want error")
	}
	if _, err := LoadRSAPublicKeys(nil); err == nil {
		t.Fatal("LoadRSAPublicKeys(nil) error = nil, want error")
	}
}
