package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type PublicationEventIDGenerator interface {
	NewUUIDv7() (domain.PublicationEventID, error)
}

type PublicationCommand struct {
	ResourceID       domain.ResourceID
	VersionID        domain.VersionID
	Action           string
	ExpectedRevision int64
	Reason           string
	EventID          domain.PublicationEventID
	Actor            shared.AuthID
	At               time.Time
}

type PublicationRepository interface {
	ApplyPublication(context.Context, PublicationCommand) (domain.PublicationResult, error)
}
