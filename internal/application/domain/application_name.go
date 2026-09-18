package domain

import "strings"

const maximumApplicationNameLength = 50

// ApplicationName retains the developer-provided display form and its
// case-insensitive uniqueness key.
type ApplicationName struct {
	value string
	key   string
}

func NewApplicationName(value string) (ApplicationName, error) {
	if len(value) == 0 || len(value) > maximumApplicationNameLength {
		return ApplicationName{}, ErrInvalidApplicationName
	}

	for index := 0; index < len(value); index++ {
		char := value[index]
		if !isASCIIAlphaNumeric(char) && char != '-' && char != '_' {
			return ApplicationName{}, ErrInvalidApplicationName
		}
	}

	return ApplicationName{value: value, key: strings.ToLower(value)}, nil
}

func (name ApplicationName) String() string {
	return name.value
}

func (name ApplicationName) Key() string {
	return name.key
}

func (name ApplicationName) valid() bool {
	validated, err := NewApplicationName(name.value)
	return err == nil && validated.key == name.key
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
