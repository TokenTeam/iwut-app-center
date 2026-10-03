package mongo

import (
	"time"

	publicationdomain "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

type applicationPublicationDocument struct {
	PublicationID   string    `bson:"publicationId"`
	ApplicationID   string    `bson:"applicationId"`
	RPCAPIMajor     int32     `bson:"rpcApiMajor"`
	TestVersionID   *string   `bson:"testVersionId"`
	StableVersionID *string   `bson:"stableVersionId"`
	Revision        int64     `bson:"revision"`
	CreatedBy       string    `bson:"createdBy"`
	CreatedAt       time.Time `bson:"createdAt"`
	UpdatedBy       string    `bson:"updatedBy"`
	UpdatedAt       time.Time `bson:"updatedAt"`
}
type applicationPublicationHistoryDocument struct {
	HistoryID              string    `bson:"historyId"`
	PublicationID          string    `bson:"publicationId"`
	ApplicationID          string    `bson:"applicationId"`
	RPCAPIMajor            int32     `bson:"rpcApiMajor"`
	PublicationRevision    int64     `bson:"publicationRevision"`
	Action                 string    `bson:"action"`
	PreviousVersionID      *string   `bson:"previousVersionId"`
	NewVersionID           *string   `bson:"newVersionId"`
	ApprovedReviewID       *string   `bson:"approvedReviewId"`
	ScopeCatalogRevision   *int64    `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion *string   `bson:"preflightPolicyVersion"`
	ChangedBy              string    `bson:"changedBy"`
	ChangedAt              time.Time `bson:"changedAt"`
}

func applicationPublicationFromDocument(d applicationPublicationDocument) (*publicationdomain.ApplicationPublication, error) {
	return publicationdomain.RestoreApplicationPublicationSlots(publicationdomain.ApplicationPublicationID(d.PublicationID), shared.ApplicationID(d.ApplicationID), d.RPCAPIMajor, publicationVersionID(d.TestVersionID), publicationVersionID(d.StableVersionID), d.Revision, shared.AuthID(d.CreatedBy), d.CreatedAt, shared.AuthID(d.UpdatedBy), d.UpdatedAt)
}
func applicationPublicationToDocument(p *publicationdomain.ApplicationPublication) applicationPublicationDocument {
	return applicationPublicationDocument{p.PublicationID().String(), p.ApplicationID().String(), p.RPCAPIMajor(), publicationVersionString(p.TestVersionIDPtr()), publicationVersionString(p.StableVersionIDPtr()), p.Revision(), p.CreatedBy().String(), p.CreatedAt(), p.UpdatedBy().String(), p.UpdatedAt()}
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
	return applicationPublicationHistoryDocument{h.HistoryID().String(), h.PublicationID().String(), h.ApplicationID().String(), h.RPCAPIMajor(), h.PublicationRevision(), string(h.Action()), previous, newVersion, review, scopeRevision, policy, h.ChangedBy().String(), h.ChangedAt()}
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
