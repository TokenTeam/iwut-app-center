package domain

import "iwut-app-center/internal/shared"

// AuthID is an opaque identity supplied by Auth.
type AuthID = shared.AuthID

func NewAuthID(value string) (AuthID, error) {
	if value == "" {
		return "", ErrDeveloperIdentityRequired
	}
	return AuthID(value), nil
}
