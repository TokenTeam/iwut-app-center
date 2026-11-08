package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

var (
	ErrApplicationPlatformSearchRequired    = errors.New("application platform search permission is required")
	ErrInvalidPlatformApplicationQuery      = errors.New("platform application query is invalid")
	ErrInvalidPlatformApplicationPageToken  = errors.New("platform application page token is invalid")
	ErrPlatformApplicationNotFound          = errors.New("platform application was not found")
	ErrPlatformApplicationStateInconsistent = errors.New("platform application state is inconsistent")
	ErrPlatformApplicationUnavailable       = errors.New("platform application query is unavailable")
)

type PlatformApplicationQuery struct {
	ApplicationID                string
	AdminAuthID                  string
	NamePrefix                   string
	LifecycleStatuses            []string
	PlatformAvailabilityStatuses []string
	ArchiveStatuses              []string
	PageSize                     int32
	PageToken                    string
}

func NewPlatformApplicationQuery(q PlatformApplicationQuery) (PlatformApplicationQuery, error) {
	bad := ErrInvalidPlatformApplicationQuery
	if q.ApplicationID != "" {
		if _, ok := shared.ParseApplicationID(q.ApplicationID); !ok {
			return PlatformApplicationQuery{}, bad
		}
	}
	if !utf8.ValidString(q.AdminAuthID) {
		return PlatformApplicationQuery{}, bad
	}
	if q.NamePrefix != "" {
		n, err := NewApplicationName(q.NamePrefix)
		if err != nil {
			return PlatformApplicationQuery{}, bad
		}
		q.NamePrefix = n.Key()
	}
	var ok bool
	if q.LifecycleStatuses, ok = platformQueryStates(q.LifecycleStatuses, []string{"ACTIVE", "CLOSING", "CLOSED"}); !ok {
		return PlatformApplicationQuery{}, bad
	}
	if q.PlatformAvailabilityStatuses, ok = platformQueryStates(q.PlatformAvailabilityStatuses, []string{"AVAILABLE", "SUSPENDED"}); !ok {
		return PlatformApplicationQuery{}, bad
	}
	if q.ArchiveStatuses, ok = platformQueryStates(q.ArchiveStatuses, []string{"UNARCHIVED", "ARCHIVED"}); !ok {
		return PlatformApplicationQuery{}, bad
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.PageSize < 1 || q.PageSize > 100 || len(q.PageToken) > 4096 || !utf8.ValidString(q.PageToken) {
		return PlatformApplicationQuery{}, bad
	}
	return q, nil
}

func platformQueryStates(input, allowed []string) ([]string, bool) {
	if len(input) == 0 {
		return nil, true
	}
	result := slices.Clone(input)
	for _, s := range result {
		if !slices.Contains(allowed, s) {
			return nil, false
		}
	}
	slices.Sort(result)
	return slices.Compact(result), true
}

type PlatformApplicationSummary struct {
	ApplicationID                shared.ApplicationID
	Name                         string
	AdminAuthID                  shared.AuthID
	CreatedAt                    time.Time
	OwnershipRevision            int64
	LifecycleStatus              ApplicationLifecycleStatus
	LifecycleRevision            int64
	PlatformAvailabilityStatus   PlatformAvailabilityStatus
	PlatformAvailabilityRevision int64
	ArchiveStatus                ApplicationArchiveStatus
	ArchiveRevision              int64
}

func (s PlatformApplicationSummary) Validate() error {
	_, err := NewApplicationName(s.Name)
	if !s.ApplicationID.IsValid() || err != nil || !s.AdminAuthID.IsValid() || !utf8.ValidString(s.AdminAuthID.String()) || !platformApplicationTime(s.CreatedAt) || s.OwnershipRevision < 1 || s.LifecycleRevision < 1 || s.PlatformAvailabilityRevision < 1 || s.ArchiveRevision < 1 || !slices.Contains([]ApplicationLifecycleStatus{ApplicationLifecycleActive, ApplicationLifecycleClosing, ApplicationLifecycleClosed}, s.LifecycleStatus) || !slices.Contains([]PlatformAvailabilityStatus{PlatformAvailabilityAvailable, PlatformAvailabilitySuspended}, s.PlatformAvailabilityStatus) || !slices.Contains([]ApplicationArchiveStatus{ApplicationArchiveUnarchived, ApplicationArchiveArchived}, s.ArchiveStatus) {
		return ErrPlatformApplicationStateInconsistent
	}
	return nil
}

func (q PlatformApplicationQuery) Matches(s PlatformApplicationSummary) bool {
	n, _ := NewApplicationName(s.Name)
	return (q.ApplicationID == "" || q.ApplicationID == s.ApplicationID.String()) && (q.AdminAuthID == "" || q.AdminAuthID == s.AdminAuthID.String()) && strings.HasPrefix(n.Key(), q.NamePrefix) && (len(q.LifecycleStatuses) == 0 || slices.Contains(q.LifecycleStatuses, string(s.LifecycleStatus))) && (len(q.PlatformAvailabilityStatuses) == 0 || slices.Contains(q.PlatformAvailabilityStatuses, string(s.PlatformAvailabilityStatus))) && (len(q.ArchiveStatuses) == 0 || slices.Contains(q.ArchiveStatuses, string(s.ArchiveStatus)))
}

type PlatformApplicationPage struct {
	Items         []PlatformApplicationSummary
	NextPageToken string
	AsOf          time.Time
}

func (p PlatformApplicationPage) Validate(q PlatformApplicationQuery, at time.Time) error {
	if p.Items == nil || len(p.Items) > int(q.PageSize) || !p.AsOf.Equal(at) || !platformApplicationTime(at) || len(p.NextPageToken) > 4096 || !utf8.ValidString(p.NextPageToken) {
		return ErrPlatformApplicationStateInconsistent
	}
	seen := make(map[shared.ApplicationID]bool, len(p.Items))
	for i, s := range p.Items {
		if s.Validate() != nil || !q.Matches(s) || seen[s.ApplicationID] {
			return ErrPlatformApplicationStateInconsistent
		}
		seen[s.ApplicationID] = true
		if i > 0 {
			prev, _ := NewApplicationName(p.Items[i-1].Name)
			current, _ := NewApplicationName(s.Name)
			if prev.Key() > current.Key() || (prev.Key() == current.Key() && p.Items[i-1].ApplicationID.String() >= s.ApplicationID.String()) {
				return ErrPlatformApplicationStateInconsistent
			}
		}
	}
	return nil
}

func platformApplicationTime(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999 && at.Equal(at.Truncate(time.Millisecond))
}
