package mongo

import (
	"time"

	publicationdomain "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

type applicationPublicationDocument struct {
	PublicationID   string               `bson:"publicationId"`
	ApplicationID   string               `bson:"applicationId"`
	RPCAPIMajor     int32                `bson:"rpcApiMajor"`
	TestVersionID   *string              `bson:"testVersionId"`
	StableVersionID *string              `bson:"stableVersionId"`
	GreyRollout     *greyRolloutDocument `bson:"greyRollout"`
	Revision        int64                `bson:"revision"`
	CreatedBy       string               `bson:"createdBy"`
	CreatedAt       time.Time            `bson:"createdAt"`
	UpdatedBy       string               `bson:"updatedBy"`
	UpdatedAt       time.Time            `bson:"updatedAt"`
}
type greyRolloutDocument struct {
	RolloutID           string `bson:"rolloutId"`
	VersionID           string `bson:"versionId"`
	ExposureBasisPoints int32  `bson:"exposureBasisPoints"`
	CohortSeed          []byte `bson:"cohortSeed"`
}
type applicationPublicationHistoryDocument struct {
	HistoryID                   string    `bson:"historyId"`
	PublicationID               string    `bson:"publicationId"`
	ApplicationID               string    `bson:"applicationId"`
	RPCAPIMajor                 int32     `bson:"rpcApiMajor"`
	PublicationRevision         int64     `bson:"publicationRevision"`
	Action                      string    `bson:"action"`
	PreviousVersionID           *string   `bson:"previousVersionId"`
	NewVersionID                *string   `bson:"newVersionId"`
	ApprovedReviewID            *string   `bson:"approvedReviewId"`
	ScopeCatalogRevision        *int64    `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion      *string   `bson:"preflightPolicyVersion"`
	GreyRolloutID               *string   `bson:"greyRolloutId"`
	PreviousExposureBasisPoints *int32    `bson:"previousExposureBasisPoints"`
	NewExposureBasisPoints      *int32    `bson:"newExposureBasisPoints"`
	CohortSeed                  []byte    `bson:"cohortSeed"`
	ChangedBy                   string    `bson:"changedBy"`
	ChangedAt                   time.Time `bson:"changedAt"`
}

func applicationPublicationFromDocument(d applicationPublicationDocument) (*publicationdomain.ApplicationPublication, error) {
	var grey *publicationdomain.GreyRollout
	if d.GreyRollout != nil {
		seed, err := publicationdomain.NewCohortSeed(d.GreyRollout.CohortSeed)
		if err != nil {
			return nil, err
		}
		grey, err = publicationdomain.RestoreGreyRollout(publicationdomain.GreyRolloutID(d.GreyRollout.RolloutID), publicationdomain.ApplicationVersionID(d.GreyRollout.VersionID), publicationdomain.ExposureBasisPoints(d.GreyRollout.ExposureBasisPoints), seed)
		if err != nil {
			return nil, err
		}
	}
	return publicationdomain.RestoreApplicationPublicationSlotsWithGrey(publicationdomain.ApplicationPublicationID(d.PublicationID), shared.ApplicationID(d.ApplicationID), d.RPCAPIMajor, publicationVersionID(d.TestVersionID), publicationVersionID(d.StableVersionID), grey, d.Revision, shared.AuthID(d.CreatedBy), d.CreatedAt, shared.AuthID(d.UpdatedBy), d.UpdatedAt)
}
func applicationPublicationToDocument(p *publicationdomain.ApplicationPublication) applicationPublicationDocument {
	var grey *greyRolloutDocument
	if rollout := p.GreyRollout(); rollout != nil {
		seed := rollout.CohortSeed().Bytes()
		grey = &greyRolloutDocument{rollout.RolloutID().String(), rollout.VersionID().String(), rollout.ExposureBasisPoints().Int32(), append([]byte(nil), seed[:]...)}
	}
	return applicationPublicationDocument{PublicationID: p.PublicationID().String(), ApplicationID: p.ApplicationID().String(), RPCAPIMajor: p.RPCAPIMajor(), TestVersionID: publicationVersionString(p.TestVersionIDPtr()), StableVersionID: publicationVersionString(p.StableVersionIDPtr()), GreyRollout: grey, Revision: p.Revision(), CreatedBy: p.CreatedBy().String(), CreatedAt: p.CreatedAt(), UpdatedBy: p.UpdatedBy().String(), UpdatedAt: p.UpdatedAt()}
}
func applicationPublicationHistoryToDocument(h *publicationdomain.ApplicationPublicationHistory) applicationPublicationHistoryDocument {
	var previous *string
	if id := h.PreviousVersionID(); id != nil {
		v := id.String()
		previous = &v
	}
	var newVersion, review, policy *string
	var scopeRevision *int64
	if id := h.NewVersionIDPtr(); id != nil {
		value := id.String()
		newVersion = &value
	}
	if id := h.ApprovedReviewIDPtr(); id != nil {
		value := id.String()
		review = &value
	}
	if value := h.ScopeCatalogRevisionPtr(); value != nil {
		revision := value.Int64()
		scopeRevision = &revision
	}
	if value := h.PreflightPolicyVersionPtr(); value != nil {
		version := value.String()
		policy = &version
	}
	var rolloutID *string
	if id := h.GreyRolloutIDPtr(); id != nil {
		value := id.String()
		rolloutID = &value
	}
	var previousExposure, newExposure *int32
	if value := h.PreviousExposureBasisPointsPtr(); value != nil {
		v := value.Int32()
		previousExposure = &v
	}
	if value := h.NewExposureBasisPointsPtr(); value != nil {
		v := value.Int32()
		newExposure = &v
	}
	var seedBytes []byte
	if seed := h.CohortSeedPtr(); seed != nil {
		value := seed.Bytes()
		seedBytes = append([]byte(nil), value[:]...)
	}
	return applicationPublicationHistoryDocument{HistoryID: h.HistoryID().String(), PublicationID: h.PublicationID().String(), ApplicationID: h.ApplicationID().String(), RPCAPIMajor: h.RPCAPIMajor(), PublicationRevision: h.PublicationRevision(), Action: string(h.Action()), PreviousVersionID: previous, NewVersionID: newVersion, ApprovedReviewID: review, ScopeCatalogRevision: scopeRevision, PreflightPolicyVersion: policy, GreyRolloutID: rolloutID, PreviousExposureBasisPoints: previousExposure, NewExposureBasisPoints: newExposure, CohortSeed: seedBytes, ChangedBy: h.ChangedBy().String(), ChangedAt: h.ChangedAt()}
}

func publicationVersionID(value *string) *publicationdomain.ApplicationVersionID {
	if value == nil {
		return nil
	}
	id := publicationdomain.ApplicationVersionID(*value)
	return &id
}

func publicationVersionString(value *publicationdomain.ApplicationVersionID) *string {
	if value == nil {
		return nil
	}
	id := value.String()
	return &id
}
