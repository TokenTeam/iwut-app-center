package transport

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

const HighRiskProofHeader = "x-iwut-high-risk-proof"

var errApplicationCloseProofStale = fmt.Errorf("%w: stale proof", domain.ErrHighRiskProofInvalid)

type applicationCloseProofClaims struct {
	Iss           *string         `json:"iss"`
	Sub           *string         `json:"sub"`
	Aud           json.RawMessage `json:"aud"`
	Iat           *float64        `json:"iat"`
	Nbf           *float64        `json:"nbf"`
	Exp           *float64        `json:"exp"`
	AuthTime      *float64        `json:"auth_time"`
	JTI           *string         `json:"jti"`
	TokenType     *string         `json:"token_type"`
	Purpose       *string         `json:"purpose"`
	ApplicationID *string         `json:"application_id"`
}

func (verifier *IdentityVerifier) VerifyApplicationCloseProof(token string, subject shared.AuthID, applicationID shared.ApplicationID) (domain.ApplicationCloseProof, error) {
	if verifier == nil || token == "" || token != strings.TrimSpace(token) || len(token) > 16*1024 || !subject.IsValid() || !applicationID.IsValid() {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	var header joseHeader
	if json.Unmarshal(headerBytes, &header) != nil || header.Typ != "iwut-app-close-reauth+jwt" || header.Alg != "RS256" || header.Kid == "" {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	key := verifier.publicKeys[header.Kid]
	if key == nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	var claims applicationCloseProofClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Iss == nil || claims.Sub == nil || claims.Iat == nil || claims.Nbf == nil || claims.Exp == nil || claims.AuthTime == nil || claims.JTI == nil || claims.TokenType == nil || claims.Purpose == nil || claims.ApplicationID == nil {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	var audience string
	if json.Unmarshal(claims.Aud, &audience) != nil || audience != "iwut-app-center" || *claims.Iss != verifier.issuer || *claims.Sub != subject.String() || *claims.ApplicationID != applicationID.String() || *claims.TokenType != "APP_CLOSE_REAUTH" || *claims.Purpose != "app.close" || !shared.IsUUIDv7(*claims.JTI) {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	for _, value := range []*float64{claims.Iat, claims.Nbf, claims.Exp, claims.AuthTime} {
		if math.Trunc(*value) != *value {
			return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
		}
	}
	if *claims.Iat != *claims.AuthTime || *claims.Nbf != *claims.Iat-30 || *claims.Exp != *claims.Iat+300 {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	now, skew := float64(verifier.clock.Now().UTC().Unix()), min(verifier.clockSkew.Seconds(), 30)
	if *claims.Nbf-skew > now || *claims.Iat > now+skew || *claims.AuthTime > now+skew {
		return domain.ApplicationCloseProof{}, domain.ErrHighRiskProofInvalid
	}
	proof := domain.ApplicationCloseProof{Subject: subject, ApplicationID: applicationID, JTI: *claims.JTI, AuthTime: time.Unix(int64(*claims.AuthTime), 0).UTC(), IssuedAt: time.Unix(int64(*claims.Iat), 0).UTC(), ExpiresAt: time.Unix(int64(*claims.Exp), 0).UTC()}
	if now > *claims.Exp+skew || now-*claims.AuthTime > (5*time.Minute).Seconds() {
		return proof, errApplicationCloseProofStale
	}
	return proof, nil
}
