package domain

import "strings"

// ApplicationID is the UUIDv7 identity of an Application.
type ApplicationID string

// ParseApplicationID validates untrusted textual input, accepting the canonical
// UUID text form with version 7 and an RFC 9562 variant. Persistence adapters
// must translate a parse failure into an internal corruption error rather than
// exposing it as caller validation.
func ParseApplicationID(value string) (ApplicationID, error) {
	if !isUUIDv7(value) {
		return "", ErrInvalidApplicationID
	}
	return ApplicationID(strings.ToLower(value)), nil
}

func (id ApplicationID) String() string {
	return string(id)
}

func (id ApplicationID) valid() bool {
	return isUUIDv7(string(id))
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}

	for index, char := range []byte(value) {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !isHex(char) {
			return false
		}
	}

	if value[14] != '7' {
		return false
	}

	variant := value[19]
	return variant == '8' || variant == '9' || variant == 'a' || variant == 'A' || variant == 'b' || variant == 'B'
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}
