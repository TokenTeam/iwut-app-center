package domain

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

const MaintainPermission = "app.script.maintain"

var (
	ErrIdentityRequired  = errors.New("user identity required")
	ErrMaintainForbidden = errors.New("script maintenance permission required")
	ErrInvalidResourceID = errors.New("invalid script resource id")
	ErrInvalidName       = errors.New("invalid script resource name")
	ErrInvalidPurpose    = errors.New("invalid script resource purpose")
	ErrInvalidRevision   = errors.New("invalid script resource revision")
	ErrNotFound          = errors.New("script resource not found")
	ErrRevisionConflict  = errors.New("script resource revision conflict")
	ErrInternal          = errors.New("script resource operation failed")
)

type ResourceID string

func ParseResourceID(raw string) (ResourceID, error) {
	if !shared.IsUUIDv7(raw) {
		return "", ErrInvalidResourceID
	}
	return ResourceID(strings.ToLower(raw)), nil
}

type Metadata struct {
	Name    string
	Purpose string
}

func NewMetadata(name, purpose string) (Metadata, error) {
	name = strings.TrimSpace(name)
	if !validText(name, 128) {
		return Metadata{}, ErrInvalidName
	}
	if !validText(purpose, 2048) {
		return Metadata{}, ErrInvalidPurpose
	}
	return Metadata{Name: name, Purpose: purpose}, nil
}

func validText(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

type Resource struct {
	ID        ResourceID
	Metadata  Metadata
	Revision  int64
	CreatedBy shared.AuthID
	CreatedAt time.Time
	UpdatedBy shared.AuthID
	UpdatedAt time.Time
}

func NewResource(id ResourceID, metadata Metadata, actor shared.AuthID, at time.Time) (Resource, error) {
	resource := Resource{
		ID: id, Metadata: metadata, Revision: 1,
		CreatedBy: actor, CreatedAt: at.UTC(), UpdatedBy: actor, UpdatedAt: at.UTC(),
	}
	return resource, resource.Validate()
}

func (r Resource) Validate() error {
	id, idErr := ParseResourceID(string(r.ID))
	metadata, metadataErr := NewMetadata(r.Metadata.Name, r.Metadata.Purpose)
	if idErr != nil || id != r.ID || metadataErr != nil || metadata != r.Metadata ||
		r.Revision < 1 || !r.CreatedBy.IsValid() || !r.UpdatedBy.IsValid() ||
		!validTime(r.CreatedAt) || !validTime(r.UpdatedAt) || r.UpdatedAt.Before(r.CreatedAt) ||
		(r.Revision == 1 && (r.CreatedBy != r.UpdatedBy || !r.CreatedAt.Equal(r.UpdatedAt))) {
		return ErrInternal
	}
	return nil
}

func validTime(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999
}

func (r Resource) Replace(expectedRevision int64, metadata Metadata, actor shared.AuthID, at time.Time) (Resource, bool, error) {
	if err := r.Validate(); err != nil {
		return Resource{}, false, err
	}
	if expectedRevision < 1 {
		return Resource{}, false, ErrInvalidRevision
	}
	if expectedRevision != r.Revision {
		return Resource{}, false, ErrRevisionConflict
	}
	if metadata == r.Metadata {
		return r, false, nil
	}
	if r.Revision == math.MaxInt64 || at.Before(r.UpdatedAt) {
		return Resource{}, false, ErrInternal
	}
	r.Metadata = metadata
	r.Revision++
	r.UpdatedBy = actor
	r.UpdatedAt = at.UTC()
	if err := r.Validate(); err != nil {
		return Resource{}, false, err
	}
	return r, true, nil
}
