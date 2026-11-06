package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type ResourceIDGenerator interface {
	NewUUIDv7() (domain.ResourceID, error)
}

type Clock interface {
	Now() time.Time
}

type ResourceRepository interface {
	// CreateResource records the resource and its creation audit atomically.
	CreateResource(context.Context, domain.Resource) (domain.Resource, error)
	// ReplaceResourceMetadata atomically compares the revision, replaces the
	// metadata, and records audit. A matching no-op returns the stored resource.
	ReplaceResourceMetadata(context.Context, ReplaceMetadata) (domain.Resource, error)
}

type ReplaceMetadata struct {
	ID               domain.ResourceID
	ExpectedRevision int64
	Metadata         domain.Metadata
	Actor            shared.AuthID
	At               time.Time
}
