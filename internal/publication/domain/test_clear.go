package domain

import (
	"math"
	"time"

	"iwut-app-center/internal/shared"
)

// TestClearCandidate contains only the structurally valid Publication. The
// previous target's runtime eligibility is deliberately not a prerequisite.
type TestClearCandidate struct{ publication *ApplicationPublication }

func NewTestClearCandidate(publication *ApplicationPublication, expectedRevision int64) (*TestClearCandidate, error) {
	if expectedRevision < 1 {
		return nil, ErrInvalidApplicationPublicationRevision
	}
	if publication == nil {
		return nil, ErrApplicationPublicationNotFound
	}
	if publication.Revision() != expectedRevision {
		return nil, ErrApplicationPublicationRevisionConflict
	}
	return &TestClearCandidate{copyPublication(publication)}, nil
}

func (c *TestClearCandidate) Publication() *ApplicationPublication {
	if c == nil {
		return nil
	}
	return copyPublication(c.publication)
}

func (c *TestClearCandidate) IsNoOp() bool {
	return c != nil && c.publication != nil && c.publication.testVersionID == nil
}

func (c *TestClearCandidate) Clear(historyID ApplicationPublicationHistoryID, admin shared.AuthID, at time.Time) (*PlaceInTestResult, error) {
	if c == nil || c.publication == nil {
		return nil, NewInternalError(nil)
	}
	if c.IsNoOp() {
		return NewNoOpResult(c.publication)
	}
	if !historyID.IsValid() || !admin.IsValid() || at.IsZero() || c.publication.revision == math.MaxInt64 {
		return nil, NewInternalError(nil)
	}
	p := copyPublication(c.publication)
	previous := copyVersionID(p.testVersionID)
	p.testVersionID = nil
	p.revision++
	p.updatedBy, p.updatedAt = admin, at.UTC()
	h := &ApplicationPublicationHistory{historyID: historyID, publicationID: p.publicationID, applicationID: p.applicationID, rpcAPIMajor: p.rpcAPIMajor, publicationRevision: p.revision, action: PublicationActionClearTestVersion, previousVersionID: previous, changedBy: admin, changedAt: at.UTC()}
	return &PlaceInTestResult{*p, h}, nil
}
