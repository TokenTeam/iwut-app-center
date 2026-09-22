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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	ServiceAuthorizationHeader = "authorization"
	minimumServiceRSAKeyBits   = 2048
)

type ServiceIdentityClock interface{ Now() time.Time }

type ServiceIdentitySignerConfig struct {
	ServiceID     string
	KID           string
	Audience      string
	TTL           time.Duration
	PrivateKeyPEM []byte
	Clock         ServiceIdentityClock
}

type ServiceIdentitySigner struct {
	serviceID  string
	kid        string
	audience   string
	ttl        time.Duration
	privateKey *rsa.PrivateKey
	clock      ServiceIdentityClock
}

func NewServiceIdentitySigner(configuration ServiceIdentitySignerConfig) (*ServiceIdentitySigner, error) {
	if configuration.ServiceID == "" || strings.TrimSpace(configuration.ServiceID) != configuration.ServiceID ||
		configuration.KID == "" || strings.TrimSpace(configuration.KID) != configuration.KID ||
		configuration.Audience == "" || strings.TrimSpace(configuration.Audience) != configuration.Audience ||
		configuration.TTL <= 0 || configuration.Clock == nil {
		return nil, errors.New("service identity signer: invalid configuration")
	}
	key, err := parseServicePrivateKey(configuration.PrivateKeyPEM)
	if err != nil || key.N == nil || key.N.BitLen() < minimumServiceRSAKeyBits {
		return nil, errors.New("service identity signer: private RSA key must be at least 2048 bits")
	}
	return &ServiceIdentitySigner{
		serviceID: configuration.ServiceID, kid: configuration.KID, audience: configuration.Audience,
		ttl: configuration.TTL, privateKey: key, clock: configuration.Clock,
	}, nil
}

func (signer *ServiceIdentitySigner) Sign() (string, error) {
	if signer == nil || signer.privateKey == nil || signer.clock == nil {
		return "", errors.New("service identity signer is unavailable")
	}
	now := signer.clock.Now().UTC().Truncate(time.Second)
	if now.IsZero() {
		return "", errors.New("service identity clock returned zero")
	}
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": signer.kid})
	if err != nil {
		return "", fmt.Errorf("marshal service identity header: %w", err)
	}
	claims, err := json.Marshal(map[string]any{
		"iss": signer.serviceID, "sub": signer.serviceID, "aud": signer.audience,
		"iat": now.Unix(), "nbf": now.Add(-5 * time.Second).Unix(), "exp": now.Add(signer.ttl).Unix(),
		"jti": uuid.NewString(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal service identity claims: %w", err)
	}
	headerPart := base64.RawURLEncoding.EncodeToString(header)
	payloadPart := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := headerPart + "." + payloadPart
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, signer.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign service identity: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (signer *ServiceIdentitySigner) UnaryClientInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	connection *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	options ...grpc.CallOption,
) error {
	token, err := signer.Sign()
	if err != nil {
		return err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(ServiceAuthorizationHeader, "Bearer "+token)
	return invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, connection, options...)
}

func parseServicePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}
