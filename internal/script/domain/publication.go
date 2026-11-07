package domain

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

const (
	PublishScript         = "PUBLISH"
	ClearScriptHead       = "CLEAR_HEAD"
	SuspendScript         = "SUSPEND"
	RestoreScript         = "RESTORE"
	DistributionAvailable = "AVAILABLE"
	DistributionSuspended = "SUSPENDED"
)

var (
	ErrPublicationForbidden         = errors.New("script publication command forbidden")
	ErrInvalidPublicationRequest    = errors.New("invalid script publication request")
	ErrPublicationRevisionConflict  = errors.New("script publication revision conflict")
	ErrAvailabilityRevisionConflict = errors.New("script availability revision conflict")
	ErrScriptNotApproved            = errors.New("script version is not approved")
	ErrDistributionSuspended        = errors.New("script distribution suspended")
	ErrPublicationInconsistent      = errors.New("inconsistent script publication state")
)

type PublicationEventID string

type PublishedScript struct {
	VersionID             VersionID
	ReviewID              ReviewID
	DecisionID            string
	Label, Format, Digest string
	ByteLength            int64
}

func (s PublishedScript) Validate() error {
	version, err := ParseVersionID(string(s.VersionID))
	review, valid := ParseReviewID(string(s.ReviewID))
	if err != nil || version != s.VersionID || !valid || review != s.ReviewID ||
		!shared.IsUUIDv7(s.DecisionID) || strings.ToLower(s.DecisionID) != s.DecisionID || ValidateVersionLabel(s.Label) != nil ||
		s.Format != JSONFormat || !shared.ValidScriptDigest(s.Digest) || s.ByteLength < 1 || s.ByteLength > MaxContentBytes {
		return ErrPublicationInconsistent
	}
	return nil
}

type PublicationState struct {
	ResourceID                                  ResourceID
	Head                                        *PublishedScript
	PublicationRevision                         int64
	Availability                                string
	AvailabilityRevision                        int64
	PublicationUpdatedBy, AvailabilityUpdatedBy shared.AuthID
	PublicationUpdatedAt, AvailabilityUpdatedAt time.Time
}

func InitialPublication(resource Resource) PublicationState {
	return PublicationState{ResourceID: resource.ID, PublicationRevision: 1, Availability: DistributionAvailable, AvailabilityRevision: 1,
		PublicationUpdatedBy: resource.CreatedBy, AvailabilityUpdatedBy: resource.CreatedBy,
		PublicationUpdatedAt: resource.CreatedAt, AvailabilityUpdatedAt: resource.CreatedAt}
}

func (s PublicationState) Validate() error {
	id, err := ParseResourceID(string(s.ResourceID))
	if err != nil || id != s.ResourceID || s.PublicationRevision < 1 || s.AvailabilityRevision < 1 ||
		!s.PublicationUpdatedBy.IsValid() || !s.AvailabilityUpdatedBy.IsValid() ||
		!validTime(s.PublicationUpdatedAt) || !validTime(s.AvailabilityUpdatedAt) ||
		(s.Availability != DistributionAvailable && s.Availability != DistributionSuspended) ||
		(s.Head != nil && s.Head.Validate() != nil) || (s.PublicationRevision == 1 && s.Head != nil) ||
		(s.AvailabilityRevision == 1 && s.Availability != DistributionAvailable) {
		return ErrPublicationInconsistent
	}
	return nil
}

func PublicationPermission(action string) string {
	switch action {
	case PublishScript, ClearScriptHead:
		return "app.script.publish"
	case SuspendScript:
		return "app.script.suspend"
	case RestoreScript:
		return "app.script.restore"
	default:
		return ""
	}
}

func NormalizeDistributionReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 2048 || !utf8.ValidString(reason) {
		return "", ErrInvalidPublicationRequest
	}
	return reason, nil
}

func (s PublicationState) CheckRevision(action string, expected int64) error {
	if expected < 1 || PublicationPermission(action) == "" {
		return ErrInvalidPublicationRequest
	}
	if action == PublishScript || action == ClearScriptHead {
		if expected != s.PublicationRevision {
			return ErrPublicationRevisionConflict
		}
	} else if expected != s.AvailabilityRevision {
		return ErrAvailabilityRevisionConflict
	}
	return nil
}

// Transition preserves independent publication and availability revisions.
// A matching no-op returns the original audit times and consumes no revision.
func (s PublicationState) Transition(action string, target *PublishedScript, expected int64, actor shared.AuthID, at time.Time) (PublicationState, bool, error) {
	if err := s.Validate(); err != nil {
		return PublicationState{}, false, err
	}
	if err := s.CheckRevision(action, expected); err != nil {
		return PublicationState{}, false, err
	}
	if !actor.IsValid() || !validTime(at) {
		return PublicationState{}, false, ErrPublicationInconsistent
	}
	switch action {
	case PublishScript, ClearScriptHead:
		if action == PublishScript {
			if target == nil || target.Validate() != nil {
				return PublicationState{}, false, ErrPublicationInconsistent
			}
			if s.Head != nil && *s.Head == *target {
				return s, false, nil
			}
			if s.Availability == DistributionSuspended {
				return PublicationState{}, false, ErrDistributionSuspended
			}
		} else if s.Head == nil {
			return s, false, nil
		}
		if s.PublicationRevision == math.MaxInt64 || at.Before(s.PublicationUpdatedAt) {
			return PublicationState{}, false, ErrPublicationInconsistent
		}
		s.Head = nil
		if action == PublishScript {
			copy := *target
			s.Head = &copy
		}
		s.PublicationRevision++
		s.PublicationUpdatedBy, s.PublicationUpdatedAt = actor, at.UTC()
	case SuspendScript, RestoreScript:
		next := DistributionSuspended
		if action == RestoreScript {
			next = DistributionAvailable
		}
		if s.Availability == next {
			return s, false, nil
		}
		if s.AvailabilityRevision == math.MaxInt64 || at.Before(s.AvailabilityUpdatedAt) {
			return PublicationState{}, false, ErrPublicationInconsistent
		}
		s.Availability, s.AvailabilityRevision = next, s.AvailabilityRevision+1
		s.AvailabilityUpdatedBy, s.AvailabilityUpdatedAt = actor, at.UTC()
	}
	return s, true, nil
}

type PublicationEvent struct {
	ID         PublicationEventID
	ResourceID ResourceID
	Action     string
	Revision   int64
	Version    *PublishedScript
	Actor      shared.AuthID
	At         time.Time
	Reason     string
}

type PublicationResult struct {
	State PublicationState
	Event *PublicationEvent
}
