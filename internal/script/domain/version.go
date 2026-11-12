package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	JSONFormat      = "json"
	MaxContentBytes = 1_048_576
)

var (
	ErrInvalidVersionID        = errors.New("invalid script version id")
	ErrInvalidVersionLabel     = errors.New("invalid script version label")
	ErrUnsupportedScriptFormat = errors.New("unsupported script format")
	ErrInvalidScriptText       = errors.New("invalid script text")
	ErrVersionNotFound         = errors.New("script version not found")
	ErrVersionLabelConflict    = errors.New("script version label already used")
	ErrVersionNotDraft         = errors.New("script version is not draft")
)

type VersionID string

func ParseVersionID(raw string) (VersionID, error) {
	if !shared.IsUUIDv7(raw) {
		return "", ErrInvalidVersionID
	}
	return VersionID(strings.ToLower(raw)), nil
}

var versionLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidateVersionLabel(label string) error {
	if !versionLabelPattern.MatchString(label) {
		return ErrInvalidVersionLabel
	}
	return nil
}

type Content struct {
	Format string
	Text   string
	Digest string
}

func NewContent(format, rawText string) (Content, error) {
	if format != JSONFormat {
		return Content{}, ErrUnsupportedScriptFormat
	}
	if !validText(rawText, MaxContentBytes) || strings.TrimSpace(rawText) == "" {
		return Content{}, ErrInvalidScriptText
	}
	sum := sha256.Sum256([]byte(rawText))
	return Content{Format: format, Text: rawText, Digest: hex.EncodeToString(sum[:])}, nil
}

type VersionState string

const (
	VersionDraft     VersionState = "DRAFT"
	VersionSubmitted VersionState = "SUBMITTED"
	VersionApproved  VersionState = "APPROVED"
	VersionRejected  VersionState = "REJECTED"
)

type Version struct {
	ID           VersionID
	ResourceID   ResourceID
	Label        string
	Content      Content
	State        VersionState
	Revision     int64
	Contributors []shared.AuthID
	CreatedBy    shared.AuthID
	CreatedAt    time.Time
	UpdatedBy    shared.AuthID
	UpdatedAt    time.Time
}

func NewDraft(id VersionID, resourceID ResourceID, label string, content Content, actor shared.AuthID, at time.Time) (Version, error) {
	draft := Version{
		ID: id, ResourceID: resourceID, Label: label, Content: content,
		State: VersionDraft, Revision: 1, Contributors: []shared.AuthID{actor},
		CreatedBy: actor, CreatedAt: at.UTC(), UpdatedBy: actor, UpdatedAt: at.UTC(),
	}
	if err := draft.Validate(); err != nil {
		return Version{}, err
	}
	return draft, nil
}

func (v Version) Validate() error {
	id, idErr := ParseVersionID(string(v.ID))
	resourceID, resourceErr := ParseResourceID(string(v.ResourceID))
	content, contentErr := NewContent(v.Content.Format, v.Content.Text)
	if idErr != nil || id != v.ID || resourceErr != nil || resourceID != v.ResourceID ||
		ValidateVersionLabel(v.Label) != nil || contentErr != nil || content != v.Content ||
		v.Revision < 1 || !v.CreatedBy.IsValid() || !v.UpdatedBy.IsValid() ||
		!validTime(v.CreatedAt) || !validTime(v.UpdatedAt) || v.UpdatedAt.Before(v.CreatedAt) {
		return ErrInternal
	}
	if !v.AuthoredBy(v.CreatedBy) {
		return ErrInternal
	}
	switch v.State {
	case VersionDraft:
		if v.Revision == 1 && !v.CreatedAt.Equal(v.UpdatedAt) {
			return ErrInternal
		}
	case VersionSubmitted, VersionApproved, VersionRejected:
		if v.Revision < 2 {
			return ErrInternal
		}
	default:
		return ErrInternal
	}
	return nil
}

// AuthoredBy checks the version's content provenance against its resource owner.
func (v Version) AuthoredBy(owner shared.AuthID) bool {
	return owner.IsValid() && v.CreatedBy == owner && len(v.Contributors) == 1 &&
		v.Contributors[0] == owner && (v.State != VersionDraft || v.UpdatedBy == owner)
}

func (v Version) CheckDraftRevision(expected int64) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if expected < 1 {
		return ErrInvalidRevision
	}
	if expected != v.Revision {
		return ErrRevisionConflict
	}
	if v.State != VersionDraft {
		return ErrVersionNotDraft
	}
	return nil
}

func (v Version) ReplaceDraft(expected int64, content Content, actor shared.AuthID, at time.Time) (Version, bool, error) {
	if err := v.CheckDraftRevision(expected); err != nil {
		return Version{}, false, err
	}
	if actor != v.CreatedBy {
		return Version{}, false, ErrNotFound
	}
	if content == v.Content {
		return v, false, nil
	}
	if v.Revision == math.MaxInt64 || at.Before(v.UpdatedAt) {
		return Version{}, false, ErrInternal
	}
	v.Content = content
	v.Revision++
	v.UpdatedBy = actor
	v.UpdatedAt = at.UTC()
	if err := v.Validate(); err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}
