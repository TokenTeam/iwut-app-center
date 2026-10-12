package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	publicationport "iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

// Latest Test action must agree with the current slot. Selecting by target
// first could resurrect an older SET after a CLEAR or a replacement.
func checkTestPublicationHistory(ctx context.Context, db *drivermongo.Database, p applicationPublicationDocument) error {
	var h applicationPublicationHistoryDocument
	raw, err := db.Collection(applicationPublicationHistoryCollectionName).FindOne(ctx, bson.M{"publicationId": p.PublicationID, "publicationRevision": bson.M{"$lte": p.Revision}, "action": bson.M{"$in": bson.A{"SET_TEST_VERSION", "CLEAR_TEST_VERSION"}}}, options.FindOne().SetSort(bson.D{{Key: "publicationRevision", Value: -1}})).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		if p.TestVersionID == nil {
			return nil
		}
		return publicationport.ErrApplicationPublicationStateInconsistent
	}
	if err != nil {
		return err
	}
	if err := bson.Unmarshal(raw, &h); err != nil {
		return publicationport.ErrApplicationPublicationStateInconsistent
	}
	if h.Action == "CLEAR_TEST_VERSION" {
		for _, field := range []string{"newVersionId", "approvedReviewId", "scopeCatalogRevision", "preflightPolicyVersion", "greyRolloutId", "previousExposureBasisPoints", "newExposureBasisPoints", "cohortSeed"} {
			if raw.Lookup(field).Type != bson.TypeNull {
				return publicationport.ErrApplicationPublicationStateInconsistent
			}
		}
	}
	return checkLatestTestHistory(p, &h)
}

func checkLatestTestHistory(p applicationPublicationDocument, h *applicationPublicationHistoryDocument) error {
	bad := publicationport.ErrApplicationPublicationStateInconsistent
	if h == nil {
		if p.TestVersionID == nil {
			return nil
		}
		return bad
	}
	if h.PublicationID != p.PublicationID || h.ApplicationID != p.ApplicationID || h.RPCAPIMajor != p.RPCAPIMajor || h.PublicationRevision < 1 || h.PublicationRevision > p.Revision || !shared.IsUUIDv7(h.HistoryID) || h.ChangedBy == "" || h.ChangedAt.IsZero() || h.GreyRolloutID != nil || h.PreviousExposureBasisPoints != nil || h.NewExposureBasisPoints != nil || h.CohortSeed != nil {
		return bad
	}
	if h.Action == "CLEAR_TEST_VERSION" {
		if p.TestVersionID != nil || h.PreviousVersionID == nil || !shared.IsUUIDv7(*h.PreviousVersionID) || h.PublicationRevision < 2 || h.NewVersionID != nil || h.ApprovedReviewID != nil || h.ScopeCatalogRevision != nil || h.PreflightPolicyVersion != nil {
			return bad
		}
		return nil
	}
	if h.Action != "SET_TEST_VERSION" || p.TestVersionID == nil || h.NewVersionID == nil || *h.NewVersionID != *p.TestVersionID || h.ApprovedReviewID == nil || !shared.IsUUIDv7(*h.ApprovedReviewID) || h.ScopeCatalogRevision == nil || *h.ScopeCatalogRevision < 1 || h.PreflightPolicyVersion == nil {
		return bad
	}
	return nil
}
