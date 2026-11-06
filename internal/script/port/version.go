package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type VersionIDGenerator interface {
	NewUUIDv7() (domain.VersionID, error)
}

type DraftRepository interface {
	// CreateDraft checks the resource and inserts the version and its audit atomically.
	CreateDraft(context.Context, domain.Version) (domain.Version, error)
	// ReplaceDraft checks the exact resource, version, state and revision, then
	// atomically replaces content, contributors and audit for a real change.
	ReplaceDraft(context.Context, ReplaceDraft) (domain.Version, error)
}

type ReplaceDraft struct {
	ResourceID       domain.ResourceID
	VersionID        domain.VersionID
	ExpectedRevision int64
	Content          domain.Content
	Actor            shared.AuthID
	At               time.Time
}
