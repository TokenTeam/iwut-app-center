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
	ErrInvalidManagementActivityQuery      = errors.New("application management activity query is invalid")
	ErrInvalidManagementActivityPageToken  = errors.New("application management activity page token is invalid")
	ErrManagementActivityNotFound          = errors.New("application management activity was not found")
	ErrManagementActivityStateInconsistent = errors.New("application management activity state is inconsistent")
	ErrManagementActivityResponseTooLarge  = errors.New("application management activity response is too large")
	ErrManagementActivityUnavailable       = errors.New("application management activity is unavailable")
)

const ManagementActivityResponseBudget = 512 * 1024

type ManagementActivityKind string

const (
	VersionReviewSubmitted ManagementActivityKind = "VERSION_REVIEW_SUBMITTED"
	VersionReviewApproved  ManagementActivityKind = "VERSION_REVIEW_APPROVED"
	VersionReviewRejected  ManagementActivityKind = "VERSION_REVIEW_REJECTED"
	ProfileReviewSubmitted ManagementActivityKind = "PROFILE_REVIEW_SUBMITTED"
	ProfileReviewApproved  ManagementActivityKind = "PROFILE_REVIEW_APPROVED"
	ProfileReviewRejected  ManagementActivityKind = "PROFILE_REVIEW_REJECTED"
)

var managementActivityKinds = []ManagementActivityKind{VersionReviewSubmitted, VersionReviewApproved, VersionReviewRejected, ProfileReviewSubmitted, ProfileReviewApproved, ProfileReviewRejected}

type ManagementActivityQuery struct {
	Kinds     []ManagementActivityKind
	PageSize  int32
	PageToken string
}

func NewManagementActivityQuery(q ManagementActivityQuery) (ManagementActivityQuery, error) {
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.PageSize < 1 || q.PageSize > 100 || len(q.PageToken) > 4096 || !utf8.ValidString(q.PageToken) {
		return ManagementActivityQuery{}, ErrInvalidManagementActivityQuery
	}
	for _, kind := range q.Kinds {
		if !slices.Contains(managementActivityKinds, kind) {
			return ManagementActivityQuery{}, ErrInvalidManagementActivityQuery
		}
	}
	canonical := make([]ManagementActivityKind, 0, 6)
	for _, kind := range managementActivityKinds {
		if len(q.Kinds) == 0 || slices.Contains(q.Kinds, kind) {
			canonical = append(canonical, kind)
		}
	}
	q.Kinds = canonical
	return q, nil
}
func (q ManagementActivityQuery) Includes(k ManagementActivityKind) bool {
	return slices.Contains(q.Kinds, k)
}

// A locator identifies an existing review and phase, never an authorization ticket.
type ManagementActivityLocator struct {
	Source   int
	Phase    int
	ReviewID string
}

func (l ManagementActivityLocator) Valid() bool {
	id, ok := shared.ParseApplicationID(l.ReviewID) // all source IDs use canonical UUIDv7
	return ok && id.String() == l.ReviewID && l.Source >= 0 && l.Source <= 1 && l.Phase >= 0 && l.Phase <= 1
}
func (l ManagementActivityLocator) String() string {
	source, phase := "version", "decision"
	if l.Source == 1 {
		source = "profile"
	}
	if l.Phase == 1 {
		phase = "submission"
	}
	return "v1." + source + "." + phase + "." + l.ReviewID
}
func ParseManagementActivityID(raw string) (ManagementActivityLocator, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return ManagementActivityLocator{}, ErrInvalidManagementActivityQuery
	}
	l := ManagementActivityLocator{Source: -1, Phase: -1, ReviewID: parts[3]}
	if parts[1] == "version" {
		l.Source = 0
	}
	if parts[1] == "profile" {
		l.Source = 1
	}
	if parts[2] == "decision" {
		l.Phase = 0
	}
	if parts[2] == "submission" {
		l.Phase = 1
	}
	if !l.Valid() || l.String() != raw {
		return ManagementActivityLocator{}, ErrInvalidManagementActivityQuery
	}
	return l, nil
}

type VersionReviewActivityTarget struct {
	VersionID, ReviewID string
	Attempt             int32
}
type ProfileReviewActivityTarget struct {
	ProfileRevisionID, ProfileReviewID string
	Attempt                            int32
}
type ManagementActivity struct {
	ActivityID               string
	ApplicationID            shared.ApplicationID
	Kind                     ManagementActivityKind
	OccurredAt               time.Time
	VersionReview            *VersionReviewActivityTarget
	ProfileReview            *ProfileReviewActivityTarget
	VersionLabelAtSubmission *string
	ProfileRevisionSequence  int64
}

func (a ManagementActivity) Locator() ManagementActivityLocator {
	l, _ := ParseManagementActivityID(a.ActivityID)
	return l
}
func (a ManagementActivity) Validate() error {
	bad := ErrManagementActivityStateInconsistent
	l, err := ParseManagementActivityID(a.ActivityID)
	if err != nil || !a.ApplicationID.IsValid() || !platformApplicationTime(a.OccurredAt) || !slices.Contains(managementActivityKinds, a.Kind) {
		return bad
	}
	if l.Source == 0 {
		v := a.VersionReview
		if v == nil || a.ProfileReview != nil || a.VersionLabelAtSubmission == nil || *a.VersionLabelAtSubmission == "" || !utf8.ValidString(*a.VersionLabelAtSubmission) || a.ProfileRevisionSequence != 0 || v.Attempt < 1 || v.ReviewID != l.ReviewID {
			return bad
		}
		if _, ok := shared.ParseApplicationID(v.VersionID); !ok {
			return bad
		}
	} else {
		p := a.ProfileReview
		if p == nil || a.VersionReview != nil || a.VersionLabelAtSubmission != nil || a.ProfileRevisionSequence < 1 || a.ProfileRevisionSequence > math.MaxInt32 || p.Attempt < 1 || p.ProfileReviewID != l.ReviewID {
			return bad
		}
		if _, ok := shared.ParseApplicationID(p.ProfileRevisionID); !ok {
			return bad
		}
	}
	if (l.Source == 0) != strings.HasPrefix(string(a.Kind), "VERSION_") || (l.Phase == 1) != strings.HasSuffix(string(a.Kind), "_SUBMITTED") {
		return bad
	}
	return nil
}

// CompareManagementActivity implements the complete order, including millisecond ties.
func CompareManagementActivity(a, b ManagementActivity) int {
	if a.OccurredAt.Before(b.OccurredAt) {
		return 1
	}
	if a.OccurredAt.After(b.OccurredAt) {
		return -1
	}
	x, y := a.Locator(), b.Locator()
	if x.Source != y.Source {
		return x.Source - y.Source
	}
	if x.Phase != y.Phase {
		return x.Phase - y.Phase
	}
	return -strings.Compare(x.ReviewID, y.ReviewID)
}

type ManagementActivityPage struct {
	Items         []ManagementActivity
	NextPageToken string
	AsOf          time.Time
}

func (p ManagementActivityPage) Validate(app shared.ApplicationID, q ManagementActivityQuery, at time.Time) error {
	bad := ErrManagementActivityStateInconsistent
	if p.Items == nil || len(p.Items) > int(q.PageSize) || !p.AsOf.Equal(at) || !platformApplicationTime(at) || len(p.NextPageToken) > 4096 || !utf8.ValidString(p.NextPageToken) || (p.NextPageToken != "" && len(p.Items) != int(q.PageSize)) {
		return bad
	}
	seen := make(map[string]bool, len(p.Items))
	for i, a := range p.Items {
		if a.Validate() != nil || a.ApplicationID != app || !q.Includes(a.Kind) || seen[a.ActivityID] || (i > 0 && CompareManagementActivity(p.Items[i-1], a) >= 0) {
			return bad
		}
		seen[a.ActivityID] = true
	}
	return nil
}

type ManagementActivityDetail struct {
	Activity        ManagementActivity
	RejectionReason *string
	AsOf            time.Time
}

func (d ManagementActivityDetail) Validate(app shared.ApplicationID, id ManagementActivityLocator, at time.Time) error {
	bad := ErrManagementActivityStateInconsistent
	if d.Activity.Validate() != nil || d.Activity.ApplicationID != app || d.Activity.ActivityID != id.String() || !d.AsOf.Equal(at) || !platformApplicationTime(at) {
		return bad
	}
	rejected := d.Activity.Kind == VersionReviewRejected || d.Activity.Kind == ProfileReviewRejected
	if rejected {
		if d.RejectionReason == nil || strings.TrimSpace(*d.RejectionReason) == "" || !utf8.ValidString(*d.RejectionReason) {
			return bad
		}
	} else if d.RejectionReason != nil {
		return bad
	}
	return nil
}
