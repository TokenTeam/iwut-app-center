package mongo

import (
	"time"

	publicationdomain "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

type applicationPublicationDocument struct {
	PublicationID string    `bson:"publicationId"`
	ApplicationID string    `bson:"applicationId"`
	RPCAPIMajor   int32     `bson:"rpcApiMajor"`
	TestVersionID string    `bson:"testVersionId"`
	Revision      int64     `bson:"revision"`
	CreatedBy     string    `bson:"createdBy"`
	CreatedAt     time.Time `bson:"createdAt"`
	UpdatedBy     string    `bson:"updatedBy"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}
type applicationPublicationHistoryDocument struct {
	HistoryID              string    `bson:"historyId"`
	PublicationID          string    `bson:"publicationId"`
	ApplicationID          string    `bson:"applicationId"`
	RPCAPIMajor            int32     `bson:"rpcApiMajor"`
	PublicationRevision    int64     `bson:"publicationRevision"`
	Action                 string    `bson:"action"`
	PreviousVersionID      *string   `bson:"previousVersionId"`
	NewVersionID           string    `bson:"newVersionId"`
	ApprovedReviewID       string    `bson:"approvedReviewId"`
	ScopeCatalogRevision   int64     `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string    `bson:"preflightPolicyVersion"`
	ChangedBy              string    `bson:"changedBy"`
	ChangedAt              time.Time `bson:"changedAt"`
}

func applicationPublicationFromDocument(d applicationPublicationDocument) (*publicationdomain.ApplicationPublication, error) {
	return publicationdomain.RestoreApplicationPublication(publicationdomain.ApplicationPublicationID(d.PublicationID), shared.ApplicationID(d.ApplicationID), d.RPCAPIMajor, publicationdomain.ApplicationVersionID(d.TestVersionID), d.Revision, shared.AuthID(d.CreatedBy), d.CreatedAt, shared.AuthID(d.UpdatedBy), d.UpdatedAt)
}
func applicationPublicationToDocument(p *publicationdomain.ApplicationPublication) applicationPublicationDocument {
	return applicationPublicationDocument{p.PublicationID().String(), p.ApplicationID().String(), p.RPCAPIMajor(), p.TestVersionID().String(), p.Revision(), p.CreatedBy().String(), p.CreatedAt(), p.UpdatedBy().String(), p.UpdatedAt()}
}
func applicationPublicationHistoryToDocument(h *publicationdomain.ApplicationPublicationHistory) applicationPublicationHistoryDocument {
	var previous *string
	if id := h.PreviousVersionID(); id != nil {
		v := id.String()
		previous = &v
	}
	return applicationPublicationHistoryDocument{h.HistoryID().String(), h.PublicationID().String(), h.ApplicationID().String(), h.RPCAPIMajor(), h.PublicationRevision(), string(h.Action()), previous, h.NewVersionID().String(), h.ApprovedReviewID().String(), h.ScopeCatalogRevision().Int64(), h.PreflightPolicyVersion().String(), h.ChangedBy().String(), h.ChangedAt()}
}
