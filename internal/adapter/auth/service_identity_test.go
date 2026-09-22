package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fixedServiceClock struct{ now time.Time }

func (clock fixedServiceClock) Now() time.Time { return clock.now }

func TestServiceIdentitySigner_SignsExpectedClaimsAndInterceptorOverwritesAuthorization(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	now := time.Date(2026, time.September, 22, 1, 2, 3, 0, time.UTC)
	signer, err := NewServiceIdentitySigner(ServiceIdentitySignerConfig{
		ServiceID: "app-center", KID: "key-1", Audience: "iwut-auth-center", TTL: time.Minute,
		PrivateKeyPEM: privatePEM, Clock: fixedServiceClock{now: now},
	})
	if err != nil {
		t.Fatalf("NewServiceIdentitySigner() error = %v", err)
	}

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer stale", "x-test", "kept"))
	var captured metadata.MD
	err = signer.UnaryClientInterceptor(ctx, "/test.Service/Call", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		captured, _ = metadata.FromOutgoingContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("UnaryClientInterceptor() error = %v", err)
	}
	if got := captured.Get("x-test"); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("x-test metadata = %v", got)
	}
	values := captured.Get("authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || values[0] == "Bearer stale" {
		t.Fatalf("authorization metadata = %v", values)
	}
	verifyServiceToken(t, strings.TrimPrefix(values[0], "Bearer "), &key.PublicKey, now)
}

func verifyServiceToken(t *testing.T, token string, key *rsa.PublicKey, now time.Time) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWS parts = %d", len(parts))
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "RS256" || header["typ"] != "JWT" || header["kid"] != "key-1" {
		t.Fatalf("header = %#v", header)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "app-center" || claims["sub"] != "app-center" || claims["aud"] != "iwut-auth-center" ||
		int64(claims["iat"].(float64)) != now.Unix() || int64(claims["exp"].(float64)) != now.Add(time.Minute).Unix() || claims["jti"] == "" {
		t.Fatalf("claims = %#v", claims)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, 0, digest[:], signature); err == nil {
		t.Fatal("signature unexpectedly verified without SHA-256 identifier")
	}
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
}
