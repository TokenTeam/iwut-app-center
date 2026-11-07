package domain

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

var (
	ErrReviewForbidden  = errors.New("script review permission required")
	ErrReviewConflict   = errors.New("script review conflict of interest")
	ErrReviewDecided    = errors.New("script review already decided")
	ErrPolicyMismatch   = errors.New("script review policy mismatch")
	ErrInvalidDecision  = errors.New("invalid script review decision")
	ErrChecksIncomplete = errors.New("script review checks incomplete")
)

type Decision struct {
	ID                string
	Outcome           VersionState
	PolicyVersion     string
	ConfirmedCheckIDs []string
	Reason            string
	Actor             shared.AuthID
	At                time.Time
}

type DecisionInput struct {
	ExpectedRevision  int64
	PolicyVersion     string
	Action            string
	ConfirmedCheckIDs []string
	Reason            string
}

func (r Review) Decide(v Version, input DecisionInput, id string, actor shared.AuthID, at time.Time) (Version, Decision, error) {
	if r.Status != ReviewPending || r.Decision != nil || v.State != VersionSubmitted {
		return Version{}, Decision{}, ErrReviewDecided
	}
	if input.ExpectedRevision != v.Revision {
		return Version{}, Decision{}, ErrRevisionConflict
	}
	if input.PolicyVersion != r.Policy.Version {
		return Version{}, Decision{}, ErrPolicyMismatch
	}
	if actor == v.CreatedBy || actor == r.SubmittedBy || slices.Contains(r.Snapshot.Contributors, actor) {
		return Version{}, Decision{}, ErrReviewConflict
	}
	if r.Policy.Validate() != nil || v.Validate() != nil || r.Snapshot.VersionID != v.ID ||
		r.Snapshot.ResourceID != v.ResourceID || r.Snapshot.Content != v.Content ||
		r.Snapshot.SourceRevision != v.Revision-1 || !slices.Equal(r.Snapshot.Contributors, v.Contributors) ||
		v.Revision == math.MaxInt64 || !shared.IsUUIDv7(id) || !actor.IsValid() || !validTime(at) || at.Before(r.SubmittedAt) {
		return Version{}, Decision{}, ErrInternal
	}
	reason := strings.TrimSpace(input.Reason)
	if !utf8.ValidString(reason) || len(reason) > 2048 {
		return Version{}, Decision{}, ErrInvalidDecision
	}
	confirmed := slices.Clone(input.ConfirmedCheckIDs)
	slices.Sort(confirmed)
	outcome := VersionApproved
	switch input.Action {
	case "APPROVE":
		known := make(map[string]bool, len(r.Policy.Checks))
		for _, c := range r.Policy.Checks {
			known[c.ID] = c.Mandatory
		}
		for i, c := range confirmed {
			if _, ok := known[c]; !ok || (i > 0 && confirmed[i-1] == c) {
				return Version{}, Decision{}, ErrChecksIncomplete
			}
			delete(known, c)
		}
		for _, mandatory := range known {
			if mandatory {
				return Version{}, Decision{}, ErrChecksIncomplete
			}
		}
	case "REJECT":
		outcome = VersionRejected
		if reason == "" || len(confirmed) != 0 {
			return Version{}, Decision{}, ErrInvalidDecision
		}
	default:
		return Version{}, Decision{}, ErrInvalidDecision
	}
	v.State, v.Revision, v.UpdatedBy, v.UpdatedAt = outcome, v.Revision+1, actor, at.UTC()
	return v, Decision{ID: id, Outcome: outcome, PolicyVersion: r.Policy.Version, ConfirmedCheckIDs: confirmed, Reason: reason, Actor: actor, At: at.UTC()}, nil
}

type CascadeProgress struct {
	TaskID, Status                    string
	Processed, Total                  int64
	Rejected, AlreadyTerminal, Closed int64
	ObservedAt                        time.Time
	LastFailure                       string
}

type DecisionResult struct {
	ReviewID ReviewID
	Revision int64
	Decision Decision
	Cascade  *CascadeProgress
}
