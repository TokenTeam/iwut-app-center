package shared

import "strings"

// ApplicationID is the UUIDv7 identity shared by capabilities that belong to
// one Application.
type ApplicationID string

// ParseApplicationID validates syntax without choosing a capability-specific
// business error. Callers map false to their own InvalidApplicationId error.
func ParseApplicationID(value string) (ApplicationID, bool) {
	if !IsUUIDv7(value) {
		return "", false
	}
	return ApplicationID(strings.ToLower(value)), true
}

func (id ApplicationID) String() string {
	return string(id)
}

func (id ApplicationID) IsValid() bool {
	return IsUUIDv7(string(id))
}

// IsUUIDv7 accepts the canonical textual UUID form, version 7 and the RFC
// 9562 variant. It intentionally does not generate IDs.
func IsUUIDv7(value string) bool {
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
