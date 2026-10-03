package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"time"

	"iwut-app-center/internal/shared"
)

type GreyRolloutID string

func (id GreyRolloutID) String() string { return string(id) }
func (id GreyRolloutID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ExposureBasisPoints int32

func NewExposureBasisPoints(value int32) (ExposureBasisPoints, error) {
	if value < 1 || value > 10_000 {
		return 0, ErrInvalidGreyExposureBasisPoints
	}
	return ExposureBasisPoints(value), nil
}
func (value ExposureBasisPoints) Int32() int32  { return int32(value) }
func (value ExposureBasisPoints) IsValid() bool { return value >= 1 && value <= 10_000 }

type CohortSeed struct{ value [32]byte }

func NewCohortSeed(value []byte) (CohortSeed, error) {
	if len(value) != sha256.Size {
		return CohortSeed{}, ErrApplicationPublicationStateInconsistent
	}
	var seed CohortSeed
	copy(seed.value[:], value)
	return seed, nil
}
func (seed CohortSeed) Bytes() [32]byte  { return seed.value }
func (seed CohortSeed) String() string   { return "[redacted grey cohort seed]" }
func (seed CohortSeed) GoString() string { return seed.String() }

type GreyRollout struct {
	rolloutID           GreyRolloutID
	versionID           ApplicationVersionID
	exposureBasisPoints ExposureBasisPoints
	cohortSeed          CohortSeed
}

func RestoreGreyRollout(id GreyRolloutID, versionID ApplicationVersionID, exposure ExposureBasisPoints, seed CohortSeed) (*GreyRollout, error) {
	if !id.IsValid() || !versionID.IsValid() || !exposure.IsValid() {
		return nil, ErrApplicationPublicationStateInconsistent
	}
	return &GreyRollout{id, versionID, exposure, seed}, nil
}
func (rollout *GreyRollout) RolloutID() GreyRolloutID        { return rollout.rolloutID }
func (rollout *GreyRollout) VersionID() ApplicationVersionID { return rollout.versionID }
func (rollout *GreyRollout) ExposureBasisPoints() ExposureBasisPoints {
	return rollout.exposureBasisPoints
}
func (rollout *GreyRollout) CohortSeed() CohortSeed { return rollout.cohortSeed }
func (rollout *GreyRollout) Matches(authID shared.AuthID) bool {
	if rollout == nil || !authID.IsValid() {
		return false
	}
	auth := []byte(authID.String())
	message := make([]byte, len("iwut-grey-v1")+1+4+len(auth))
	copy(message, "iwut-grey-v1")
	offset := len("iwut-grey-v1") + 1
	binary.BigEndian.PutUint32(message[offset:offset+4], uint32(len(auth)))
	copy(message[offset+4:], auth)
	mac := hmac.New(sha256.New, rollout.cohortSeed.value[:])
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	bucket := binary.BigEndian.Uint64(digest[:8]) % 10_000
	return bucket < uint64(rollout.exposureBasisPoints)
}

func copyGreyRollout(value *GreyRollout) *GreyRollout {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

type GreyChangeKind string

const (
	GreyChangeNoOp     GreyChangeKind = "NO_OP"
	GreyChangeStart    GreyChangeKind = "START"
	GreyChangeIncrease GreyChangeKind = "INCREASE"
	GreyChangeDecrease GreyChangeKind = "DECREASE"
	GreyChangeReplace  GreyChangeKind = "REPLACE"
)

func ClassifyGreyChange(publication *ApplicationPublication, versionID ApplicationVersionID, exposure ExposureBasisPoints, expectedRevision int64) (GreyChangeKind, error) {
	if !versionID.IsValid() {
		return "", ErrInvalidApplicationVersionId
	}
	if !exposure.IsValid() {
		return "", ErrInvalidGreyExposureBasisPoints
	}
	if expectedRevision < 1 {
		return "", ErrInvalidApplicationPublicationRevision
	}
	if publication == nil {
		return "", ErrApplicationPublicationNotFound
	}
	if publication.revision != expectedRevision {
		return "", ErrApplicationPublicationRevisionConflict
	}
	if publication.stableVersionID == nil {
		if publication.greyRollout != nil {
			return "", ErrApplicationPublicationStateInconsistent
		}
		return "", ErrGreyStableBaselineRequired
	}
	current := publication.greyRollout
	if current == nil {
		return GreyChangeStart, nil
	}
	if current.versionID != versionID {
		return GreyChangeReplace, nil
	}
	if current.exposureBasisPoints == exposure {
		return GreyChangeNoOp, nil
	}
	if exposure > current.exposureBasisPoints {
		return GreyChangeIncrease, nil
	}
	return GreyChangeDecrease, nil
}

type GreyPlacementCandidate struct {
	applicationID    shared.ApplicationID
	rpcAPIMajor      int32
	versionID        ApplicationVersionID
	exposure         ExposureBasisPoints
	publication      *ApplicationPublication
	expectedRevision int64
	change           GreyChangeKind
	approved         *TestPlacementCandidate
}

func NewGreyPlacementCandidate(approved *TestPlacementCandidate, exposure ExposureBasisPoints) (*GreyPlacementCandidate, error) {
	if approved == nil || approved.expectedPublicationRevision == nil {
		return nil, NewInternalError(nil)
	}
	change, err := ClassifyGreyChange(approved.publication, approved.versionID, exposure, *approved.expectedPublicationRevision)
	if err != nil {
		return nil, err
	}
	return &GreyPlacementCandidate{approved.applicationID, approved.rpcAPIMajor, approved.versionID, exposure, copyPublication(approved.publication), *approved.expectedPublicationRevision, change, approved}, nil
}

func NewGreyReductionCandidate(app shared.ApplicationID, major int32, versionID ApplicationVersionID, exposure ExposureBasisPoints, publication *ApplicationPublication, expectedRevision int64) (*GreyPlacementCandidate, error) {
	if !app.IsValid() || major < 1 || publication == nil || publication.applicationID != app || publication.rpcAPIMajor != major {
		return nil, NewInternalError(nil)
	}
	change, err := ClassifyGreyChange(publication, versionID, exposure, expectedRevision)
	if err != nil {
		return nil, err
	}
	if change != GreyChangeNoOp && change != GreyChangeDecrease {
		return nil, NewInternalError(nil)
	}
	return &GreyPlacementCandidate{app, major, versionID, exposure, copyPublication(publication), expectedRevision, change, nil}, nil
}

func (c *GreyPlacementCandidate) ApplicationID() shared.ApplicationID      { return c.applicationID }
func (c *GreyPlacementCandidate) RPCAPIMajor() int32                       { return c.rpcAPIMajor }
func (c *GreyPlacementCandidate) VersionID() ApplicationVersionID          { return c.versionID }
func (c *GreyPlacementCandidate) ExposureBasisPoints() ExposureBasisPoints { return c.exposure }
func (c *GreyPlacementCandidate) Publication() *ApplicationPublication {
	return copyPublication(c.publication)
}
func (c *GreyPlacementCandidate) ExpectedPublicationRevision() int64 { return c.expectedRevision }
func (c *GreyPlacementCandidate) ChangeKind() GreyChangeKind         { return c.change }
func (c *GreyPlacementCandidate) IsNoOp() bool                       { return c != nil && c.change == GreyChangeNoOp }
func (c *GreyPlacementCandidate) RequiresValidation() bool {
	return c != nil && c.change != GreyChangeNoOp && c.change != GreyChangeDecrease
}
func (c *GreyPlacementCandidate) ApprovedCandidate() *TestPlacementCandidate {
	if c == nil {
		return nil
	}
	return c.approved
}

func (c *GreyPlacementCandidate) SetGrey(rolloutID *GreyRolloutID, seed *CohortSeed, historyID ApplicationPublicationHistoryID, admin shared.AuthID, validation *PublicationValidation, at time.Time) (*PlaceInTestResult, error) {
	if c == nil || c.publication == nil {
		return nil, NewInternalError(nil)
	}
	if c.IsNoOp() {
		return NewNoOpResult(c.publication)
	}
	if !historyID.IsValid() || !admin.IsValid() || at.IsZero() || c.publication.revision == math.MaxInt64 {
		return nil, NewInternalError(nil)
	}
	if c.RequiresValidation() && (validation == nil || !validation.Valid()) || !c.RequiresValidation() && validation != nil {
		return nil, NewInternalError(nil)
	}
	publication := copyPublication(c.publication)
	previousRollout := publication.greyRollout
	var next *GreyRollout
	if c.change == GreyChangeStart {
		if rolloutID == nil || seed == nil || !rolloutID.IsValid() {
			return nil, NewInternalError(nil)
		}
		var err error
		next, err = RestoreGreyRollout(*rolloutID, c.versionID, c.exposure, *seed)
		if err != nil {
			return nil, NewInternalError(err)
		}
	} else {
		if rolloutID != nil || seed != nil || previousRollout == nil {
			return nil, NewInternalError(nil)
		}
		next = &GreyRollout{previousRollout.rolloutID, c.versionID, c.exposure, previousRollout.cohortSeed}
	}
	publication.greyRollout = next
	publication.revision++
	publication.updatedBy = admin
	publication.updatedAt = at.UTC()
	action := map[GreyChangeKind]PublicationAction{GreyChangeStart: PublicationActionSetGreyRollout, GreyChangeIncrease: PublicationActionIncreaseGrey, GreyChangeDecrease: PublicationActionDecreaseGrey, GreyChangeReplace: PublicationActionReplaceGreyVersion}[c.change]
	newVersion, newExposure, id := c.versionID, c.exposure, next.rolloutID
	var previousVersion *ApplicationVersionID
	var previousExposure *ExposureBasisPoints
	if previousRollout != nil {
		v, e := previousRollout.versionID, previousRollout.exposureBasisPoints
		previousVersion, previousExposure = &v, &e
	}
	var review *ApplicationReviewID
	var validationCopy *PublicationValidation
	if c.RequiresValidation() {
		r, v := c.approved.reviewID, *validation
		review, validationCopy = &r, &v
	}
	var historySeed *CohortSeed
	if c.change == GreyChangeStart {
		s := *seed
		historySeed = &s
	}
	history := &ApplicationPublicationHistory{historyID: historyID, publicationID: publication.publicationID, applicationID: c.applicationID, rpcAPIMajor: c.rpcAPIMajor, publicationRevision: publication.revision, action: action, previousVersionID: previousVersion, newVersionID: &newVersion, approvedReviewID: review, validation: validationCopy, greyRolloutID: &id, previousExposure: previousExposure, newExposure: &newExposure, cohortSeed: historySeed, changedBy: admin, changedAt: at.UTC()}
	return &PlaceInTestResult{publication: *publication, history: history}, nil
}

type GreyClearCandidate struct{ publication *ApplicationPublication }

func NewGreyClearCandidate(publication *ApplicationPublication, expectedRevision int64) (*GreyClearCandidate, error) {
	if expectedRevision < 1 {
		return nil, ErrInvalidApplicationPublicationRevision
	}
	if publication == nil {
		return nil, ErrApplicationPublicationNotFound
	}
	if publication.revision != expectedRevision {
		return nil, ErrApplicationPublicationRevisionConflict
	}
	if publication.stableVersionID == nil {
		if publication.greyRollout != nil {
			return nil, ErrApplicationPublicationStateInconsistent
		}
		return nil, ErrGreyStableBaselineRequired
	}
	return &GreyClearCandidate{copyPublication(publication)}, nil
}
func (c *GreyClearCandidate) Publication() *ApplicationPublication {
	if c == nil {
		return nil
	}
	return copyPublication(c.publication)
}
func (c *GreyClearCandidate) IsNoOp() bool {
	return c != nil && c.publication != nil && c.publication.greyRollout == nil
}
func (c *GreyClearCandidate) Clear(historyID ApplicationPublicationHistoryID, admin shared.AuthID, at time.Time) (*PlaceInTestResult, error) {
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
	previous := publication.greyRollout
	publication.greyRollout = nil
	publication.revision++
	publication.updatedBy, publication.updatedAt = admin, at.UTC()
	previousVersion, previousExposure, id := previous.versionID, previous.exposureBasisPoints, previous.rolloutID
	history := &ApplicationPublicationHistory{historyID: historyID, publicationID: publication.publicationID, applicationID: publication.applicationID, rpcAPIMajor: publication.rpcAPIMajor, publicationRevision: publication.revision, action: PublicationActionClearGreyRollout, previousVersionID: &previousVersion, greyRolloutID: &id, previousExposure: &previousExposure, changedBy: admin, changedAt: at.UTC()}
	return &PlaceInTestResult{publication: *publication, history: history}, nil
}
