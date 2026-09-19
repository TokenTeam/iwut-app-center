package domain

import "iwut-app-center/internal/shared"

// ApplicationID is the UUIDv7 identity of an Application.
type ApplicationID = shared.ApplicationID

// ParseApplicationID validates untrusted textual input, accepting the canonical
// UUID text form with version 7 and an RFC 9562 variant. Persistence adapters
// must translate a parse failure into an internal corruption error rather than
// exposing it as caller validation.
func ParseApplicationID(value string) (ApplicationID, error) {
	id, ok := shared.ParseApplicationID(value)
	if !ok {
		return "", ErrInvalidApplicationID
	}
	return id, nil
}
