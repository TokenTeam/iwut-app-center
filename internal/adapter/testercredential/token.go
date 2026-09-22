// Package testercredential implements credential generation and URL packaging.
// It has no network dependencies and never persists or logs credentials.
package testercredential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	testerport "iwut-app-center/internal/tester/port"
)

type SecureTesterJoinTokenFactory struct{ reader io.Reader }

func NewSecureTesterJoinTokenFactory() *SecureTesterJoinTokenFactory {
	return &SecureTesterJoinTokenFactory{rand.Reader}
}
func (f *SecureTesterJoinTokenFactory) NewToken() (string, [32]byte, error) {
	var secret [32]byte
	if f == nil || f.reader == nil {
		return "", [32]byte{}, errors.New("secure token source unavailable")
	}
	if _, err := io.ReadFull(f.reader, secret[:]); err != nil {
		return "", [32]byte{}, errors.New("secure token generation failed")
	}
	hash := sha256.Sum256(secret[:])
	return base64.RawURLEncoding.EncodeToString(secret[:]), hash, nil
}

var _ testerport.SecureTesterJoinTokenFactory = (*SecureTesterJoinTokenFactory)(nil)
