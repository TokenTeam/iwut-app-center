package domain

import "time"

type ApplicationCore struct {
	ApplicationID, Name, AdminID string
	CreatedAt                    time.Time
	OwnershipRevision            int64
	LifecycleStatus              string
	LifecycleRevision            int64
	PlatformAvailabilityStatus   string
	PlatformAvailabilityRevision int64
}
type Counts struct {
	VersionCount, DraftVersionCount, SubmittedVersionCount, ApprovedVersionCount, RejectedVersionCount int64
	PendingVersionReviewCount, PendingProfileReviewCount, ActiveTesterCount                            int64
}
type Summary struct {
	Application ApplicationCore
	Counts      Counts
}
type Page struct {
	Items         []Summary
	NextPageToken string
	AsOf          time.Time
}
type ProfileState struct{ WorkingProfileRevisionID, CurrentPublishedProfileRevisionID *string }
type Publication struct {
	RPCAPIMajor                                   int32
	Revision                                      int64
	TestVersionID, GreyVersionID, StableVersionID *string
	GreyRolloutPercent                            int32
}
type FilterState struct {
	Revision            int64
	FilterRevisionID    *string
	Sequence            int64
	SchemaVersion, Mode string
}
type OAuthClient struct {
	ClientID, Status    string
	AuthorizationEpoch  int64
	CredentialRevision  *int64
	CredentialRotatedAt *time.Time
}
type OAuthRegistration struct {
	Channel                          string
	RegistrationRevision             int64
	PublicClient, ConfidentialClient *OAuthClient
}
type PendingTransfer struct {
	TransferID, ToAdminID  string
	RequestedAt, ExpiresAt time.Time
}
type Closure struct {
	ClosureID, Status, AuthRevocationState string
	ClosingStartedAt                       time.Time
	ClosedAt                               *time.Time
}
type Detail struct {
	Application        ApplicationCore
	Counts             Counts
	ProfileState       ProfileState
	Publications       []Publication
	FilterState        FilterState
	OAuthRegistrations []OAuthRegistration
	PendingTransfer    *PendingTransfer
	Closure            *Closure
	AsOf               time.Time
}
