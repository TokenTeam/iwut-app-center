package oauthcredential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"

	"iwut-app-center/internal/oauthclient/domain"
)

const secretDomain = "iwut-oauth-client-secret-v1\x00"

type SecretFactory struct{}

func NewSecretFactory() *SecretFactory { return &SecretFactory{} }

func (*SecretFactory) NewSecret(clientID domain.ClientID) (string, domain.SecretDigest, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", domain.SecretDigest{}, err
	}
	plain := base64.RawURLEncoding.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(secretDomain + clientID.String() + "\x00" + plain))
	return plain, domain.NewSecretDigest(digest), nil
}
