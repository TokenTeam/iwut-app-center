package domain

import (
	"iwut-app-center/internal/shared"
	"math"
	"regexp"
	"sort"
	"time"
)

type ApplicationVersionID string
type ApplicationReviewID string
type ApplicationPublicationID string
type ApplicationPublicationHistoryID string

func (id ApplicationVersionID) String() string            { return string(id) }
func (id ApplicationVersionID) IsValid() bool             { return shared.IsUUIDv7(string(id)) }
func (id ApplicationReviewID) String() string             { return string(id) }
func (id ApplicationReviewID) IsValid() bool              { return shared.IsUUIDv7(string(id)) }
func (id ApplicationPublicationID) String() string        { return string(id) }
func (id ApplicationPublicationID) IsValid() bool         { return shared.IsUUIDv7(string(id)) }
func (id ApplicationPublicationHistoryID) String() string { return string(id) }
func (id ApplicationPublicationHistoryID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ScopeName string
type LaunchURL string
type ScopeCatalogRevision int64

func (v ScopeCatalogRevision) Int64() int64 { return int64(v) }

type PreflightPolicyVersion string

func (v PreflightPolicyVersion) String() string { return string(v) }

var policyVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,50}$`)

func NewPreflightPolicyVersion(v string) (PreflightPolicyVersion, error) {
	if !policyVersionPattern.MatchString(v) {
		return "", NewInternalError(nil)
	}
	return PreflightPolicyVersion(v), nil
}
func NewScopeCatalogRevision(v int64) (ScopeCatalogRevision, error) {
	if v < 1 {
		return 0, NewInternalError(nil)
	}
	return ScopeCatalogRevision(v), nil
}

type PublicationValidation struct {
	ScopeCatalogRevision   ScopeCatalogRevision
	PreflightPolicyVersion PreflightPolicyVersion
}

func (v PublicationValidation) Valid() bool {
	return v.ScopeCatalogRevision > 0 && policyVersionPattern.MatchString(v.PreflightPolicyVersion.String())
}

type ApplicationPublication struct {
	publicationID   ApplicationPublicationID
	applicationID   shared.ApplicationID
	rpcAPIMajor     int32
	testVersionID   *ApplicationVersionID
	stableVersionID *ApplicationVersionID
	revision        int64
	createdBy       shared.AuthID
	createdAt       time.Time
	updatedBy       shared.AuthID
	updatedAt       time.Time
}

func RestoreApplicationPublication(id ApplicationPublicationID, app shared.ApplicationID, major int32, version ApplicationVersionID, revision int64, createdBy shared.AuthID, createdAt time.Time, updatedBy shared.AuthID, updatedAt time.Time) (*ApplicationPublication, error) {
	return RestoreApplicationPublicationSlots(id, app, major, &version, nil, revision, createdBy, createdAt, updatedBy, updatedAt)
}

func RestoreApplicationPublicationSlots(id ApplicationPublicationID, app shared.ApplicationID, major int32, testVersionID, stableVersionID *ApplicationVersionID, revision int64, createdBy shared.AuthID, createdAt time.Time, updatedBy shared.AuthID, updatedAt time.Time) (*ApplicationPublication, error) {
	if !id.IsValid() || !app.IsValid() || major < 1 || revision < 1 || !createdBy.IsValid() || !updatedBy.IsValid() || createdAt.IsZero() || updatedAt.IsZero() || (testVersionID != nil && !testVersionID.IsValid()) || (stableVersionID != nil && !stableVersionID.IsValid()) {
		return nil, NewInternalError(nil)
	}
	return &ApplicationPublication{id, app, major, copyVersionID(testVersionID), copyVersionID(stableVersionID), revision, createdBy, createdAt.UTC(), updatedBy, updatedAt.UTC()}, nil
}
func (p *ApplicationPublication) PublicationID() ApplicationPublicationID { return p.publicationID }
func (p *ApplicationPublication) ApplicationID() shared.ApplicationID     { return p.applicationID }
func (p *ApplicationPublication) RPCAPIMajor() int32                      { return p.rpcAPIMajor }
func (p *ApplicationPublication) TestVersionID() ApplicationVersionID {
	if p.testVersionID == nil {
		return ""
	}
	return *p.testVersionID
}
func (p *ApplicationPublication) TestVersionIDPtr() *ApplicationVersionID {
	return copyVersionID(p.testVersionID)
}
func (p *ApplicationPublication) StableVersionID() ApplicationVersionID {
	if p.stableVersionID == nil {
		return ""
	}
	return *p.stableVersionID
}
func (p *ApplicationPublication) StableVersionIDPtr() *ApplicationVersionID {
	return copyVersionID(p.stableVersionID)
}
func (p *ApplicationPublication) Revision() int64          { return p.revision }
func (p *ApplicationPublication) CreatedBy() shared.AuthID { return p.createdBy }
func (p *ApplicationPublication) CreatedAt() time.Time     { return p.createdAt }
func (p *ApplicationPublication) UpdatedBy() shared.AuthID { return p.updatedBy }
func (p *ApplicationPublication) UpdatedAt() time.Time     { return p.updatedAt }
func copyPublication(p *ApplicationPublication) *ApplicationPublication {
	if p == nil {
		return nil
	}
	v := *p
	v.testVersionID = copyVersionID(p.testVersionID)
	v.stableVersionID = copyVersionID(p.stableVersionID)
	return &v
}

func copyVersionID(id *ApplicationVersionID) *ApplicationVersionID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}

type TestPlacementCandidate struct {
	applicationID               shared.ApplicationID
	rpcAPIMajor                 int32
	versionID                   ApplicationVersionID
	reviewID                    ApplicationReviewID
	versionRevision             int64
	snapshot                    ApplicationVersionReviewSnapshot
	publication                 *ApplicationPublication
	expectedPublicationRevision *int64
}

func NewTestPlacementCandidate(app shared.ApplicationID, major int32, version ApplicationVersionID, review ApplicationReviewID, versionRevision int64, snapshot ApplicationVersionReviewSnapshot, publication *ApplicationPublication, expectedRevision *int64) (*TestPlacementCandidate, error) {
	if !app.IsValid() || major < 1 || !version.IsValid() || !review.IsValid() || versionRevision < 3 || snapshot.launchURL == "" {
		return nil, NewInternalError(nil)
	}
	if major < snapshot.rpcAPIMinVersion || major >= snapshot.rpcAPIMaxVersionExclusive {
		return nil, ErrApplicationVersionRpcApiIncompatible
	}
	if expectedRevision != nil && *expectedRevision < 1 {
		return nil, ErrInvalidApplicationPublicationRevision
	}
	if publication != nil && (publication.ApplicationID() != app || publication.RPCAPIMajor() != major) {
		return nil, NewInternalError(nil)
	}
	if expectedRevision == nil && publication != nil {
		return nil, ErrApplicationPublicationAlreadyExists
	}
	if expectedRevision != nil && publication == nil {
		return nil, ErrApplicationPublicationNotFound
	}
	if publication != nil && publication.Revision() != *expectedRevision {
		return nil, ErrApplicationPublicationRevisionConflict
	}
	var expected *int64
	if expectedRevision != nil {
		v := *expectedRevision
		expected = &v
	}
	return &TestPlacementCandidate{app, major, version, review, versionRevision, snapshot, copyPublication(publication), expected}, nil
}
func (c *TestPlacementCandidate) ApplicationID() shared.ApplicationID        { return c.applicationID }
func (c *TestPlacementCandidate) RPCAPIMajor() int32                         { return c.rpcAPIMajor }
func (c *TestPlacementCandidate) VersionID() ApplicationVersionID            { return c.versionID }
func (c *TestPlacementCandidate) ApprovedReviewID() ApplicationReviewID      { return c.reviewID }
func (c *TestPlacementCandidate) ReviewID() ApplicationReviewID              { return c.reviewID }
func (c *TestPlacementCandidate) VersionRevision() int64                     { return c.versionRevision }
func (c *TestPlacementCandidate) Snapshot() ApplicationVersionReviewSnapshot { return c.snapshot }
func (c *TestPlacementCandidate) Publication() *ApplicationPublication {
	return copyPublication(c.publication)
}
func (c *TestPlacementCandidate) ExpectedPublicationRevision() *int64 {
	if c.expectedPublicationRevision == nil {
		return nil
	}
	v := *c.expectedPublicationRevision
	return &v
}
func (c *TestPlacementCandidate) IsNoOp() bool {
	return c.publication != nil && c.publication.testVersionID != nil && *c.publication.testVersionID == c.versionID
}
func (c *TestPlacementCandidate) AllScopes() []ScopeName {
	v := append(c.snapshot.RequiredScopes(), c.snapshot.OptionalScopes()...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v
}

type PublicationAction string

const (
	PublicationActionSetTestVersion     PublicationAction = "SET_TEST_VERSION"
	PublicationActionSetStableVersion   PublicationAction = "SET_STABLE_VERSION"
	PublicationActionClearStableVersion PublicationAction = "CLEAR_STABLE_VERSION"
)

type ApplicationPublicationHistory struct {
	historyID           ApplicationPublicationHistoryID
	publicationID       ApplicationPublicationID
	applicationID       shared.ApplicationID
	rpcAPIMajor         int32
	publicationRevision int64
	action              PublicationAction
	previousVersionID   *ApplicationVersionID
	newVersionID        *ApplicationVersionID
	approvedReviewID    *ApplicationReviewID
	validation          *PublicationValidation
	changedBy           shared.AuthID
	changedAt           time.Time
}

func (h *ApplicationPublicationHistory) HistoryID() ApplicationPublicationHistoryID {
	return h.historyID
}
func (h *ApplicationPublicationHistory) PublicationID() ApplicationPublicationID {
	return h.publicationID
}
func (h *ApplicationPublicationHistory) ApplicationID() shared.ApplicationID { return h.applicationID }
func (h *ApplicationPublicationHistory) RPCAPIMajor() int32                  { return h.rpcAPIMajor }
func (h *ApplicationPublicationHistory) PublicationRevision() int64          { return h.publicationRevision }
func (h *ApplicationPublicationHistory) Action() PublicationAction           { return h.action }
func (h *ApplicationPublicationHistory) PreviousVersionID() *ApplicationVersionID {
	if h.previousVersionID == nil {
		return nil
	}
	v := *h.previousVersionID
	return &v
}
func (h *ApplicationPublicationHistory) NewVersionID() ApplicationVersionID {
	if h.newVersionID == nil {
		return ""
	}
	return *h.newVersionID
}
func (h *ApplicationPublicationHistory) NewVersionIDPtr() *ApplicationVersionID {
	return copyVersionID(h.newVersionID)
}
func (h *ApplicationPublicationHistory) ApprovedReviewID() ApplicationReviewID {
	if h.approvedReviewID == nil {
		return ""
	}
	return *h.approvedReviewID
}
func (h *ApplicationPublicationHistory) ApprovedReviewIDPtr() *ApplicationReviewID {
	if h.approvedReviewID == nil {
		return nil
	}
	v := *h.approvedReviewID
	return &v
}
func (h *ApplicationPublicationHistory) ScopeCatalogRevision() ScopeCatalogRevision {
	if h.validation == nil {
		return 0
	}
	return h.validation.ScopeCatalogRevision
}
func (h *ApplicationPublicationHistory) ScopeCatalogRevisionPtr() *ScopeCatalogRevision {
	if h.validation == nil {
		return nil
	}
	v := h.validation.ScopeCatalogRevision
	return &v
}
func (h *ApplicationPublicationHistory) PreflightPolicyVersion() PreflightPolicyVersion {
	if h.validation == nil {
		return ""
	}
	return h.validation.PreflightPolicyVersion
}
func (h *ApplicationPublicationHistory) PreflightPolicyVersionPtr() *PreflightPolicyVersion {
	if h.validation == nil {
		return nil
	}
	v := h.validation.PreflightPolicyVersion
	return &v
}
func (h *ApplicationPublicationHistory) ChangedBy() shared.AuthID { return h.changedBy }
func (h *ApplicationPublicationHistory) ChangedAt() time.Time     { return h.changedAt }

type PlaceInTestResult struct {
	publication ApplicationPublication
	history     *ApplicationPublicationHistory
}

func (r *PlaceInTestResult) Publication() *ApplicationPublication {
	return copyPublication(&r.publication)
}
func (r *PlaceInTestResult) History() *ApplicationPublicationHistory {
	if r.history == nil {
		return nil
	}
	v := *r.history
	v.previousVersionID = r.history.PreviousVersionID()
	v.newVersionID = r.history.NewVersionIDPtr()
	v.approvedReviewID = r.history.ApprovedReviewIDPtr()
	if r.history.validation != nil {
		validation := *r.history.validation
		v.validation = &validation
	}
	return &v
}
func (r *PlaceInTestResult) Changed() bool { return r.history != nil }
func NewNoOpResult(publication *ApplicationPublication) (*PlaceInTestResult, error) {
	if publication == nil {
		return nil, NewInternalError(nil)
	}
	return &PlaceInTestResult{publication: *publication}, nil
}

// PlaceInTest constructs a new immutable state and audit without mutating the
// candidate or its previous publication. The repository persists them atomically.
func (c *TestPlacementCandidate) PlaceInTest(publicationID *ApplicationPublicationID, historyID ApplicationPublicationHistoryID, admin shared.AuthID, validation PublicationValidation, at time.Time) (*PlaceInTestResult, error) {
	if c == nil {
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
		publication = &ApplicationPublication{*publicationID, c.applicationID, c.rpcAPIMajor, &version, nil, 1, admin, at.UTC(), admin, at.UTC()}
	} else {
		if publicationID != nil || c.publication.revision == math.MaxInt64 {
			return nil, NewInternalError(nil)
		}
		publication = copyPublication(c.publication)
		previous = copyVersionID(publication.testVersionID)
		version := c.versionID
		publication.testVersionID = &version
		publication.revision++
		publication.updatedBy = admin
		publication.updatedAt = at.UTC()
	}
	version := c.versionID
	review := c.reviewID
	validationCopy := validation
	history := &ApplicationPublicationHistory{historyID, publication.publicationID, c.applicationID, c.rpcAPIMajor, publication.revision, PublicationActionSetTestVersion, previous, &version, &review, &validationCopy, admin, at.UTC()}
	return &PlaceInTestResult{*publication, history}, nil
}
