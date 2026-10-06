package domain

import (
	"errors"
	"time"

	"iwut-app-center/internal/shared"
)

type ApplicationClosureID string

func ParseApplicationClosureID(value string) (ApplicationClosureID, error) {
	if !shared.IsUUIDv7(value) {
		return "", ErrInvalidApplicationClosureID
	}
	return ApplicationClosureID(value), nil
}
func (id ApplicationClosureID) String() string { return string(id) }
func (id ApplicationClosureID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ApplicationClosureStatus string

const (
	ApplicationClosureClosing ApplicationClosureStatus = "CLOSING"
	ApplicationClosureClosed  ApplicationClosureStatus = "CLOSED"
)

type AuthRevocationState string

const (
	AuthRevocationPending AuthRevocationState = "PENDING"
	AuthRevocationApplied AuthRevocationState = "APPLIED"
)

type ApplicationClosure struct {
	ClosureID               ApplicationClosureID
	ApplicationID           shared.ApplicationID
	InitiatedBy             shared.AuthID
	SourceOwnershipRevision int64
	SourceLifecycleRevision int64
	Status                  ApplicationClosureStatus
	HighRiskProofJTI        string
	ClosingStartedAt        time.Time
	AuthRevocationState     AuthRevocationState
	AuthReceiptID           string
	AuthAppliedAt           *time.Time
	ClosedAt                *time.Time
	LifecycleRevision       int64
}

type ApplicationClosurePreview struct {
	ApplicationID                 shared.ApplicationID
	OwnershipRevision             int64
	LifecycleRevision             int64
	LifecycleStatus               ApplicationLifecycleStatus
	HasPendingAdminTransfer       bool
	ActivePublicationChannelCount int32
	EnabledOAuthClientCount       int32
	ActiveTesterCount             int32
	PendingReviewCount            int32
}

type ApplicationCloseProof struct {
	Subject       shared.AuthID
	ApplicationID shared.ApplicationID
	JTI           string
	AuthTime      time.Time
	IssuedAt      time.Time
	ExpiresAt     time.Time
}

var (
	ErrInvalidApplicationClosureID         = errors.New("invalid application closure ID")
	ErrInvalidLifecycleRevision            = errors.New("invalid lifecycle revision")
	ErrInvalidCloseConfirmation            = errors.New("invalid close confirmation")
	ErrHighRiskProofRequired               = errors.New("high-risk proof required")
	ErrHighRiskProofInvalid                = errors.New("high-risk proof invalid")
	ErrHighRiskProofReplayed               = errors.New("high-risk proof replayed")
	ErrApplicationLifecycleChanged         = errors.New("application lifecycle changed")
	ErrApplicationNotActive                = errors.New("application is not active")
	ErrApplicationClosureNotFound          = errors.New("application closure not found")
	ErrApplicationClosureStateInconsistent = errors.New("application closure state inconsistent")
)
