package shared

// AuthID is an opaque identity supplied by Auth.
type AuthID string

func (id AuthID) String() string {
	return string(id)
}

func (id AuthID) IsValid() bool {
	return id != ""
}

// DeveloperStatus is the Auth-owned developer approval state consumed by App
// Center use cases.
type DeveloperStatus string

const (
	DeveloperStatusPending   DeveloperStatus = "PENDING"
	DeveloperStatusApproved  DeveloperStatus = "APPROVED"
	DeveloperStatusRejected  DeveloperStatus = "REJECTED"
	DeveloperStatusSuspended DeveloperStatus = "SUSPENDED"
)

// DeveloperIdentity contains only the trusted identity facts needed by the
// current App Center command handlers.
type DeveloperIdentity struct {
	AuthID          AuthID
	DeveloperStatus DeveloperStatus
}

// TrustedIdentity is the transport-verified projection of trusted-identity-v1.
// Capability transports narrow it before entering a use case.
type TrustedIdentity struct {
	AuthID          AuthID
	DeveloperStatus DeveloperStatus
	Permissions     []string
}

// AuthenticatedUserIdentity contains only the verified caller subject.
type AuthenticatedUserIdentity struct {
	AuthID AuthID
}
