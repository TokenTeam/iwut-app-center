package transport

import (
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

const closeProofApplicationID = "018f0f4e-7b2a-7def-8f5d-f38c817a1c21"
const closeProofJTI = "018f0f4e-7b2a-7def-8f5d-f38c817a1c22"

func validCloseProofClaims() map[string]any {
	now := fixedNow()
	return map[string]any{"iss": testIssuer, "sub": tokenSubject, "aud": "iwut-app-center", "iat": now.Unix(), "auth_time": now.Unix(), "nbf": now.Unix() - 30, "exp": now.Unix() + 300, "jti": closeProofJTI, "token_type": "APP_CLOSE_REAUTH", "purpose": "app.close", "application_id": closeProofApplicationID}
}

func TestApplicationCloseProof_BR_APP_021_RequiresExactContract(t *testing.T) {
	t.Parallel()
	appID, _ := shared.ParseApplicationID(closeProofApplicationID)
	token := signToken(t, tokenOptions{typ: "iwut-app-close-reauth+jwt", claims: validCloseProofClaims()})
	proof, err := newTestVerifier(t).VerifyApplicationCloseProof(token, shared.AuthID(tokenSubject), appID)
	if err != nil {
		t.Fatalf("VerifyApplicationCloseProof() error = %v", err)
	}
	if proof.JTI != closeProofJTI || proof.ApplicationID != appID || proof.Subject != tokenSubject {
		t.Fatalf("proof = %#v", proof)
	}
}

func TestApplicationCloseProof_BR_APP_021_RejectsNonCanonicalVariants(t *testing.T) {
	t.Parallel()
	appID, _ := shared.ParseApplicationID(closeProofApplicationID)
	cases := map[string]func(map[string]any){
		"audience array":       func(c map[string]any) { c["aud"] = []string{"iwut-app-center"} },
		"wrong purpose":        func(c map[string]any) { c["purpose"] = "other" },
		"fractional time":      func(c map[string]any) { c["iat"] = float64(fixedNow().Unix()) + .5 },
		"different auth time":  func(c map[string]any) { c["auth_time"] = fixedNow().Unix() - 1 },
		"different not before": func(c map[string]any) { c["nbf"] = fixedNow().Unix() - 29 },
		"different expiry":     func(c map[string]any) { c["exp"] = fixedNow().Unix() + 299 },
		"older than five minutes": func(c map[string]any) {
			issued := fixedNow().Add(-5*time.Minute - time.Second).Unix()
			c["iat"], c["auth_time"], c["nbf"], c["exp"] = issued, issued, issued-30, issued+300
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claims := validCloseProofClaims()
			mutate(claims)
			token := signToken(t, tokenOptions{typ: "iwut-app-close-reauth+jwt", claims: claims})
			_, err := newTestVerifier(t).VerifyApplicationCloseProof(token, shared.AuthID(tokenSubject), appID)
			if !errors.Is(err, domain.ErrHighRiskProofInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
