package domain

import (
	"errors"
	"time"

	"iwut-app-center/internal/shared"
)

const ApplicationAdminTransferLifetime = 7 * 24 * time.Hour

type ApplicationAdminTransferID string

func ParseApplicationAdminTransferID(value string) (ApplicationAdminTransferID, error) {
	if !shared.IsUUIDv7(value) {
		return "", ErrInvalidApplicationAdminTransferID
	}
	return ApplicationAdminTransferID(value), nil
}
func (id ApplicationAdminTransferID) String() string { return string(id) }
func (id ApplicationAdminTransferID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ApplicationAdminTransferStatus string

const (
	ApplicationAdminTransferPending   ApplicationAdminTransferStatus = "PENDING"
	ApplicationAdminTransferAccepted  ApplicationAdminTransferStatus = "ACCEPTED"
	ApplicationAdminTransferRejected  ApplicationAdminTransferStatus = "REJECTED"
	ApplicationAdminTransferCancelled ApplicationAdminTransferStatus = "CANCELLED"
	ApplicationAdminTransferExpired   ApplicationAdminTransferStatus = "EXPIRED"
)

type ConfidentialCredentialHandling string

const (
	ConfidentialCredentialKeep   ConfidentialCredentialHandling = "KEEP"
	ConfidentialCredentialRotate ConfidentialCredentialHandling = "ROTATE"
)

type ApplicationAdminTransferResolutionCause string

const (
	ApplicationAdminTransferResolutionExplicit           ApplicationAdminTransferResolutionCause = "EXPLICIT"
	ApplicationAdminTransferResolutionApplicationClosure ApplicationAdminTransferResolutionCause = "APPLICATION_CLOSURE"
)

func ParseConfidentialCredentialHandling(value string) (ConfidentialCredentialHandling, error) {
	handling := ConfidentialCredentialHandling(value)
	if handling != ConfidentialCredentialKeep && handling != ConfidentialCredentialRotate {
		return "", ErrInvalidConfidentialCredentialHandling
	}
	return handling, nil
}

type ApplicationOwnership struct {
	ApplicationID     shared.ApplicationID
	OwnershipRevision int64
	PendingTransferID *ApplicationAdminTransferID
	PendingExpiresAt  *time.Time
}

type ApplicationAdminTransfer struct {
	TransferID                     ApplicationAdminTransferID
	ApplicationID                  shared.ApplicationID
	FromAdminID                    shared.AuthID
	ToAdminID                      shared.AuthID
	SourceOwnershipRevision        int64
	Status                         ApplicationAdminTransferStatus
	RequestedAt                    time.Time
	ExpiresAt                      time.Time
	ResolvedAt                     *time.Time
	ResolvedBy                     *shared.AuthID
	ResolutionCause                *ApplicationAdminTransferResolutionCause
	ConfidentialCredentialHandling *ConfidentialCredentialHandling
}

func (t ApplicationAdminTransfer) IsParticipant(authID shared.AuthID) bool {
	return authID.IsValid() && (authID == t.FromAdminID || authID == t.ToAdminID)
}

type RotatedCredential struct {
	Channel            string
	ClientID           string
	CredentialRevision int64
	ClientSecret       string
}

type AcceptApplicationAdminTransferResult struct {
	Transfer           ApplicationAdminTransfer
	RotatedCredentials []RotatedCredential
	SecretsDisclosed   bool
}

var (
	ErrInvalidApplicationAdminTransferID           = errors.New("invalid application administrator transfer ID")
	ErrInvalidTargetAuthID                         = errors.New("invalid target Auth ID")
	ErrInvalidOwnershipRevision                    = errors.New("invalid ownership revision")
	ErrInvalidConfidentialCredentialHandling       = errors.New("invalid confidential credential handling")
	ErrApplicationAdminTransferNotFound            = errors.New("application administrator transfer not found")
	ErrApplicationNotFound                         = errors.New("application not found")
	ErrApplicationAdminRequired                    = errors.New("application administrator required")
	ErrApplicationAdminTransferParticipantRequired = errors.New("application administrator transfer participant required")
	ErrApplicationAdminTransferAlreadyPending      = errors.New("application administrator transfer already pending")
	ErrApplicationNameConflict                     = errors.New("application name conflict")
	ErrApplicationAdminTransferExpired             = errors.New("application administrator transfer expired")
	ErrApplicationAdminTransferNotPending          = errors.New("application administrator transfer is not pending")
	ErrApplicationOwnershipChanged                 = errors.New("application ownership changed")
	ErrSourceDeveloperIneligible                   = errors.New("source developer is ineligible")
	ErrTargetDeveloperIneligible                   = errors.New("target developer is ineligible")
	ErrDeveloperStatusUnavailable                  = errors.New("developer status unavailable")
	ErrApplicationAdminTransferStateInconsistent   = errors.New("application administrator transfer state inconsistent")
)
