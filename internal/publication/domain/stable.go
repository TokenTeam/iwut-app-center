package domain

import (
	"math"
	"time"

	"iwut-app-center/internal/shared"
)

// StablePlacementCandidate carries the same approved immutable snapshot used
// by test placement, while applying the change to the stable slot.
type StablePlacementCandidate struct{ *TestPlacementCandidate }

func NewStablePlacementCandidate(app shared.ApplicationID, major int32, version ApplicationVersionID, review ApplicationReviewID, versionRevision int64, snapshot ApplicationVersionReviewSnapshot, publication *ApplicationPublication, expectedRevision *int64) (*StablePlacementCandidate, error) {
	candidate, err := NewTestPlacementCandidate(app, major, version, review, versionRevision, snapshot, publication, expectedRevision)
	if err != nil {
		return nil, err
	}
	return &StablePlacementCandidate{candidate}, nil
}

func (c *StablePlacementCandidate) IsNoOp() bool {
	return c != nil && c.publication != nil && c.publication.stableVersionID != nil && *c.publication.stableVersionID == c.versionID
}

func (c *StablePlacementCandidate) SetStable(publicationID *ApplicationPublicationID, historyID ApplicationPublicationHistoryID, admin shared.AuthID, validation PublicationValidation, at time.Time) (*PlaceInTestResult, error) {
	if c == nil || c.TestPlacementCandidate == nil {
		return nil, NewInternalError(nil)
	}
	if c.IsNoOp() {
		return NewNoOpResult(c.publication)
	}
	if !historyID.IsValid() || !admin.IsValid() || !validation.Valid() || at.IsZero() {
		return nil, NewInternalError(nil)
	}
	var publication *ApplicationPublication
	var previous *ApplicationVersionID
	if c.publication == nil {
		if publicationID == nil || !publicationID.IsValid() {
			return nil, NewInternalError(nil)
		}
		version := c.versionID
		publication = &ApplicationPublication{*publicationID, c.applicationID, c.rpcAPIMajor, nil, &version, 1, admin, at.UTC(), admin, at.UTC()}
	} else {
		if publicationID != nil || c.publication.revision == math.MaxInt64 {
			return nil, NewInternalError(nil)
		}
		publication = copyPublication(c.publication)
		previous = copyVersionID(publication.stableVersionID)
		version := c.versionID
		publication.stableVersionID = &version
		publication.revision++
		publication.updatedBy = admin
		publication.updatedAt = at.UTC()
	}
	version := c.versionID
	review := c.reviewID
	validationCopy := validation
	history := &ApplicationPublicationHistory{historyID, publication.publicationID, c.applicationID, c.rpcAPIMajor, publication.revision, PublicationActionSetStableVersion, previous, &version, &review, &validationCopy, admin, at.UTC()}
	return &PlaceInTestResult{*publication, history}, nil
}

type StableClearCandidate struct {
	publication *ApplicationPublication
}

func NewStableClearCandidate(publication *ApplicationPublication, expectedRevision int64) (*StableClearCandidate, error) {
	if expectedRevision < 1 {
		return nil, ErrInvalidApplicationPublicationRevision
	}
	if publication == nil {
		return nil, ErrApplicationPublicationNotFound
	}
	if publication.Revision() != expectedRevision {
		return nil, ErrApplicationPublicationRevisionConflict
	}
	return &StableClearCandidate{copyPublication(publication)}, nil
}

func (c *StableClearCandidate) Publication() *ApplicationPublication {
	if c == nil {
		return nil
	}
	return copyPublication(c.publication)
}

func (c *StableClearCandidate) IsNoOp() bool {
	return c != nil && c.publication != nil && c.publication.stableVersionID == nil
}

func (c *StableClearCandidate) Clear(historyID ApplicationPublicationHistoryID, admin shared.AuthID, at time.Time) (*PlaceInTestResult, error) {
	if c == nil || c.publication == nil {
		return nil, NewInternalError(nil)
	}
	if c.IsNoOp() {
		return NewNoOpResult(c.publication)
	}
	if !historyID.IsValid() || !admin.IsValid() || at.IsZero() || c.publication.revision == math.MaxInt64 {
		return nil, NewInternalError(nil)
	}
	publication := copyPublication(c.publication)
	previous := copyVersionID(publication.stableVersionID)
	publication.stableVersionID = nil
	publication.revision++
	publication.updatedBy = admin
	publication.updatedAt = at.UTC()
	history := &ApplicationPublicationHistory{historyID, publication.publicationID, publication.applicationID, publication.rpcAPIMajor, publication.revision, PublicationActionClearStableVersion, previous, nil, nil, nil, admin, at.UTC()}
	return &PlaceInTestResult{*publication, history}, nil
}
