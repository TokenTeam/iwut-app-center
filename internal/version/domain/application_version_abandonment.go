package domain

import (
	"time"

	"iwut-app-center/internal/shared"
)

// ApplicationVersionAbandonmentID identifies the immutable audit fact created
// when an unsubmitted draft is permanently removed.
type ApplicationVersionAbandonmentID string

func (id ApplicationVersionAbandonmentID) String() string { return string(id) }
func (id ApplicationVersionAbandonmentID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

// ApplicationVersionAbandonment intentionally contains no deleted runtime
// content. AbandonedBy is retained for persistence audit only and transport
// adapters must never disclose it.
type ApplicationVersionAbandonment struct {
	id             ApplicationVersionAbandonmentID
	applicationID  shared.ApplicationID
	versionID      ApplicationVersionID
	sequence       VersionSequence
	versionLabel   VersionLabel
	sourceRevision int64
	abandonedBy    shared.AuthID
	abandonedAt    time.Time
}

func NewApplicationVersionAbandonment(
	id ApplicationVersionAbandonmentID,
	applicationID shared.ApplicationID,
	versionID ApplicationVersionID,
	sequence VersionSequence,
	versionLabel VersionLabel,
	sourceRevision int64,
	abandonedBy shared.AuthID,
	abandonedAt time.Time,
) (*ApplicationVersionAbandonment, error) {
	if !id.IsValid() || !applicationID.IsValid() || !versionID.IsValid() || sequence < 1 ||
		!versionLabel.valid() || sourceRevision < 1 || !abandonedBy.IsValid() || abandonedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &ApplicationVersionAbandonment{
		id: id, applicationID: applicationID, versionID: versionID, sequence: sequence,
		versionLabel: versionLabel, sourceRevision: sourceRevision, abandonedBy: abandonedBy,
		abandonedAt: abandonedAt.UTC().Truncate(time.Millisecond),
	}, nil
}

func (a *ApplicationVersionAbandonment) ID() ApplicationVersionAbandonmentID { return a.id }
func (a *ApplicationVersionAbandonment) ApplicationID() shared.ApplicationID { return a.applicationID }
func (a *ApplicationVersionAbandonment) VersionID() ApplicationVersionID     { return a.versionID }
func (a *ApplicationVersionAbandonment) Sequence() VersionSequence           { return a.sequence }
func (a *ApplicationVersionAbandonment) VersionLabel() VersionLabel          { return a.versionLabel }
func (a *ApplicationVersionAbandonment) SourceRevision() int64               { return a.sourceRevision }
func (a *ApplicationVersionAbandonment) AbandonedBy() shared.AuthID          { return a.abandonedBy }
func (a *ApplicationVersionAbandonment) AbandonedAt() time.Time              { return a.abandonedAt }

type AbandonApplicationVersionDraftResult struct {
	Abandonment *ApplicationVersionAbandonment
	Changed     bool
}
