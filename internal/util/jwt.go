package util

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

/* Security note:
- You said you'd like to provide the private key as plain PEM via environment variable. That's technically fine (these helpers accept plain PEM text), but it has security implications: environment variables can be leaked via process listings, crash logs, CI logs, or OS-level snapshots. Prefer using filesystem with strict permissions or a KMS when possible.
- These helpers intentionally accept plain PEM bytes (no base64) to match your preference. Ensure the value in env is the raw PEM block including the "-----BEGIN ...-----" header and newlines.
*/

// JwtUtilFunctions is a minimal interface that exposes the raw PEM bytes for private/public keys.
// Signing/verification logic lives elsewhere (internal/util/jwt.go) and should use these keys.
type JwtUtilFunctions interface {
	PrivateKeyPEM() []byte
	PublicKeyPEM() []byte
	EncodeJWTWithRS256(claims map[string]interface{}, ttl time.Duration) (string, error)
	DecodeJWTWithRS256(tokenStr string) (map[string]interface{}, error)
	DecodeJWT(tokenStr string) (map[string]interface{}, error)
	WithTokenValue(ctx context.Context, value *TokenValue) context.Context
	TokenValueFrom(ctx context.Context) TokenValue
	ToBaseAuthClaims(claims map[string]interface{}) (*BaseAuthClaims, error)
}

// JwtUtil stores PEM key material and implements JwtUtilFunctions.
type JwtUtil struct {
}

var (
	JwtUtilInstance *JwtUtil
)

func NewJwtUtil() *JwtUtil {
	if JwtUtilInstance != nil {
		return JwtUtilInstance
	}
	JwtUtilInstance = &JwtUtil{}
	return JwtUtilInstance
}

type TokenKey struct{}

type TokenValue struct {
	BaseAuthClaims *BaseAuthClaims
	OAuthClaims    *OAuthClaims
}
type BaseAuthClaims struct {
	Uid     string `json:"Uid"`
	Iat     int64  `json:"Iat"`
	Exp     int64  `json:"Exp"`
	Iss     string `json:"Iss"`
	Version int    `json:"Version"`
	Type    string `json:"Type"`
}
type OAuthClaims struct {
	Jti   string   `json:"Jti"`
	Uid   string   `json:"Uid"`
	Scope string   `json:"Scope"`
	Iat   int64    `json:"Iat"`
	Exp   int64    `json:"Exp"`
	Iss   string   `json:"Iss"`
	Azp   string   `json:"Azp"`
	Aud   []string `json:"Aud"`
	Type  string   `json:"Type"`
	Nonce string   `json:"Nonce"`
}
type ServiceClaims struct {
	ServiceName string `json:"ServiceName"`
	FuncName    string `json:"FuncName"`
}
type JwtType int

const (
	UnknownJwt JwtType = iota
	OfficialJwt
	OAuthJwt
	ServiceRequest
)

func (j *JwtUtil) GetJwtTypeFromClaims(claims map[string]interface{}) JwtType {
	if _, found := claims["version"]; found {
		return OfficialJwt
	}
	return OAuthJwt
}

func (j *JwtUtil) IsAccessToken(claims BaseAuthClaims) bool {
	return claims.Type == "access"
}
func (j *JwtUtil) IsRefreshToken(claims BaseAuthClaims) bool {
	return claims.Type == "refresh"
}

// BaseAuthClaimsFromJSON parses a BaseAuthClaims instance from a JSON string.
// The input JSON is expected to use capitalized keys like the example:
// {"Uid":"...","Iat":123,"Exp":456,"Iss":"...","Version":1,"Type":"access"}
func BaseAuthClaimsFromJSON(data string) (*BaseAuthClaims, error) {
	var c BaseAuthClaims
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// OAuthClaimsFromJSON parses an OAuthClaims instance from a JSON string.
// The input JSON is expected to use capitalized keys like the example:
// {"Jti":"...","Uid":"...","Scope":"read","Iat":123,"Exp":456,"Iss":"...","Azp":"...","Aud":["..."],"Type":"access","Nonce":"..."}
func OAuthClaimsFromJSON(data string) (*OAuthClaims, error) {
	var c OAuthClaims
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func ServiceClaimsFromJSON(data string) (*ServiceClaims, error) {
	var c ServiceClaims
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (j *JwtUtil) WithTokenValue(ctx context.Context, value *TokenValue) context.Context {
	return context.WithValue(ctx, TokenKey{}, value)
}
func (j *JwtUtil) TokenValueFrom(ctx context.Context) *TokenValue {
	if v := ctx.Value(TokenKey{}); v != nil {
		switch s := v.(type) {
		case TokenValue:
			return &s
		case *TokenValue:
			return s
		}
	}
	return nil
}
func (j *JwtUtil) GetBaseAuthClaims(ctx context.Context) (*BaseAuthClaims, error) {
	value := j.TokenValueFrom(ctx)
	if value == nil {
		return nil, errors.New("no token claims found in context")
	}
	if value.BaseAuthClaims != nil {
		return value.BaseAuthClaims, nil
	}
	if value.OAuthClaims != nil {
		return nil, errors.New("token is OAuth type, requested BaseAuthClaims")
	}
	return nil, errors.New("no token claims found in context")
}
func (j *JwtUtil) GetOAuthClaims(ctx context.Context) (*OAuthClaims, error) {
	value := j.TokenValueFrom(ctx)
	if value == nil {
		return nil, errors.New("no token claims found in context")
	}
	if value.OAuthClaims != nil {
		return value.OAuthClaims, nil
	}
	if value.BaseAuthClaims != nil {
		return nil, errors.New("token is BaseAuth type, requested OAuthClaims")
	}
	return nil, errors.New("no token claims found in context")
}
