package transport

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
)

// IdentityHeader is the single carrier for the trusted identity JWS over both
// HTTP headers and gRPC metadata. See platform/contracts/trusted-identity-v1.md.
const IdentityHeader = "x-iwut-identity"

// minimumRSAKeyBits is the smallest accepted RSA public key. RS256 with a
// shorter modulus is rejected at startup.
const minimumRSAKeyBits = 2048

var (
	// errIdentityRequired means no identity carrier was present.
	errIdentityRequired = errors.New("trusted identity is required")
	// errIdentityInvalid covers every present-but-untrustworthy identity. The
	// detailed cause is never exposed to the caller.
	errIdentityInvalid = errors.New("trusted identity is invalid")
)

// Clock is the local time source used for token lifetime checks.
type Clock interface {
	Now() time.Time
}

// IdentityConfig is validated at startup by NewIdentityVerifier.
type IdentityConfig struct {
	Issuer     string
	Audience   string
	MaxTTL     time.Duration
	ClockSkew  time.Duration
	PublicKeys map[string]*rsa.PublicKey
	Clock      Clock
}

// IdentityVerifier verifies a compact RS256 JWS issued by Auth Center and
// returns the trusted facts the current use cases consume. It performs no
// network or database access.
type IdentityVerifier struct {
	issuer     string
	audience   string
	maxTTL     time.Duration
	clockSkew  time.Duration
	publicKeys map[string]*rsa.PublicKey
	clock      Clock
}

func NewIdentityVerifier(config IdentityConfig) (*IdentityVerifier, error) {
	if strings.TrimSpace(config.Issuer) == "" {
		return nil, errors.New("identity verifier: issuer is required")
	}
	if strings.TrimSpace(config.Audience) == "" {
		return nil, errors.New("identity verifier: audience is required")
	}
	if config.MaxTTL <= 0 {
		return nil, errors.New("identity verifier: maximum TTL must be positive")
	}
	if config.ClockSkew < 0 {
		return nil, errors.New("identity verifier: clock skew must not be negative")
	}
	if len(config.PublicKeys) == 0 {
		return nil, errors.New("identity verifier: at least one public key is required")
	}
	for kid, key := range config.PublicKeys {
		if strings.TrimSpace(kid) == "" || key == nil {
			return nil, fmt.Errorf("identity verifier: public key %q is invalid", kid)
		}
		if key.N == nil || key.N.BitLen() < minimumRSAKeyBits {
			return nil, fmt.Errorf("identity verifier: public key %q must be at least %d bits", kid, minimumRSAKeyBits)
		}
	}
	if config.Clock == nil {
		return nil, errors.New("identity verifier: clock is required")
	}

	return &IdentityVerifier{
		issuer:     config.Issuer,
		audience:   config.Audience,
		maxTTL:     config.MaxTTL,
		clockSkew:  config.ClockSkew,
		publicKeys: config.PublicKeys,
		clock:      config.Clock,
	}, nil
}

// LoadRSAPublicKeys reads one PEM file per kid. PKIX and PKCS#1 RSA public keys
// are accepted; anything else fails startup.
func LoadRSAPublicKeys(files map[string]string) (map[string]*rsa.PublicKey, error) {
	if len(files) == 0 {
		return nil, errors.New("identity public keys: at least one key file is required")
	}
	keys := make(map[string]*rsa.PublicKey, len(files))
	for kid, path := range files {
		if strings.TrimSpace(kid) == "" || strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("identity public keys: kid %q has an empty key path", kid)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("identity public keys: read %q: %w", kid, err)
		}
		key, err := parseRSAPublicKeyPEM(data)
		if err != nil {
			return nil, fmt.Errorf("identity public keys: parse %q: %w", kid, err)
		}
		keys[kid] = key
	}
	return keys, nil
}

func parseRSAPublicKeyPEM(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaKey, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("PEM does not contain an RSA public key")
		}
		return rsaKey, nil
	}
	if rsaKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return rsaKey, nil
	}
	return nil, errors.New("PEM is not a supported RSA public key")
}

type joseHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type identityClaims struct {
	Iss             *string         `json:"iss"`
	Sub             *string         `json:"sub"`
	Aud             json.RawMessage `json:"aud"`
	Iat             *float64        `json:"iat"`
	Nbf             *float64        `json:"nbf"`
	Exp             *float64        `json:"exp"`
	Jti             *string         `json:"jti"`
	DeveloperStatus *string         `json:"developer_status"`
	Permissions     *[]string       `json:"permissions"`
}

// Verify applies the documented validation order: structure, JOSE header, key
// selection, signature, then claims and time.
func (verifier *IdentityVerifier) Verify(token string) (shared.TrustedIdentity, error) {
	if verifier == nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if strings.TrimSpace(token) == "" {
		return shared.TrustedIdentity{}, errIdentityRequired
	}
	if token != strings.TrimSpace(token) {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	var header joseHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if header.Typ != "JWT" || header.Alg != "RS256" || header.Kid == "" {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	publicKey, ok := verifier.publicKeys[header.Kid]
	if !ok || publicKey == nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	var claims identityClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if claims.Iss == nil || claims.Sub == nil || claims.Jti == nil ||
		claims.Iat == nil || claims.Nbf == nil || claims.Exp == nil {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if *claims.Iss != verifier.issuer {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if !audienceContains(claims.Aud, verifier.audience) {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if *claims.Sub == "" || *claims.Jti == "" {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	var developerStatus shared.DeveloperStatus
	if claims.DeveloperStatus != nil {
		status, ok := parseDeveloperStatus(*claims.DeveloperStatus)
		if !ok {
			return shared.TrustedIdentity{}, errIdentityInvalid
		}
		developerStatus = status
	}
	permissions := []string(nil)
	if claims.Permissions != nil {
		permissions = make([]string, 0, len(*claims.Permissions))
		seen := make(map[string]struct{}, len(*claims.Permissions))
		for _, permission := range *claims.Permissions {
			if permission == "" || strings.TrimSpace(permission) != permission {
				return shared.TrustedIdentity{}, errIdentityInvalid
			}
			if _, duplicate := seen[permission]; duplicate {
				return shared.TrustedIdentity{}, errIdentityInvalid
			}
			seen[permission] = struct{}{}
			permissions = append(permissions, permission)
		}
	}

	if *claims.Exp <= *claims.Iat || *claims.Exp <= *claims.Nbf {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if *claims.Exp-*claims.Iat > verifier.maxTTL.Seconds() {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	now := float64(verifier.clock.Now().UTC().Unix())
	skew := verifier.clockSkew.Seconds()
	if now > *claims.Exp+skew {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if *claims.Nbf-skew > now {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	if *claims.Iat > now+skew {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}

	return shared.TrustedIdentity{
		AuthID:          shared.AuthID(*claims.Sub),
		DeveloperStatus: developerStatus,
		Permissions:     permissions,
	}, nil
}

func audienceContains(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == want
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, audience := range many {
			if audience == want {
				return true
			}
		}
	}
	return false
}

func parseDeveloperStatus(value string) (shared.DeveloperStatus, bool) {
	status := shared.DeveloperStatus(value)
	switch status {
	case shared.DeveloperStatusPending,
		shared.DeveloperStatusApproved,
		shared.DeveloperStatusRejected,
		shared.DeveloperStatusSuspended:
		return status, true
	default:
		return "", false
	}
}
