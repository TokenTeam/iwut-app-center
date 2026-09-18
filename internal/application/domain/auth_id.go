package domain

// AuthID is an opaque identity supplied by Auth.
type AuthID string

func NewAuthID(value string) (AuthID, error) {
	if value == "" {
		return "", ErrDeveloperIdentityRequired
	}
	return AuthID(value), nil
}

func (id AuthID) String() string {
	return string(id)
}

func (id AuthID) valid() bool {
	return id != ""
}
