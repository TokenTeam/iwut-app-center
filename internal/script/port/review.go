package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type ReviewIDGenerator interface {
	NewUUIDv7() (domain.ReviewID, error)
}

type ReviewSubmissionRepository interface {
	// SubmitDraft binds the current review policy and source snapshot in the
	// same transaction that consumes the expected DRAFT revision.
	SubmitDraft(context.Context, SubmitDraft) (domain.Submission, error)
}

type SubmitDraft struct {
	ResourceID       domain.ResourceID
	VersionID        domain.VersionID
	ReviewID         domain.ReviewID
	ExpectedRevision int64
	Actor            shared.AuthID
	At               time.Time
}
