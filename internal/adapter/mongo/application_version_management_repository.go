package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

const (
	applicationVersionManagementMigrationID = "0027_application_version_management_query"
	applicationVersionManagementIndexName   = "ix_application_versions_application_review_sequence_version"
)

type ApplicationVersionManagementRepository struct{ database *drivermongo.Database }

func NewApplicationVersionManagementRepository(database *drivermongo.Database) *ApplicationVersionManagementRepository {
	return &ApplicationVersionManagementRepository{database: database}
}

var _ versionport.ManagementQueryRepository = (*ApplicationVersionManagementRepository)(nil)

type applicationVersionManagementCursor struct {
	Version     int    `json:"v"`
	Fingerprint string `json:"f"`
	Sequence    int32  `json:"s"`
	VersionID   string `json:"i"`
}

func (r *ApplicationVersionManagementRepository) List(ctx context.Context, adminID shared.AuthID, applicationID shared.ApplicationID, reviewStatus *versiondomain.ReviewStatus, pageSize int32, pageToken string) (*versiondomain.OwnedVersionPage, error) {
	if r == nil || r.database == nil || !adminID.IsValid() || !applicationID.IsValid() || pageSize < 1 {
		return nil, fmt.Errorf("list application versions: invalid input")
	}
	cursor, err := decodeApplicationVersionManagementCursor(pageToken, adminID, applicationID, reviewStatus)
	if err != nil {
		return nil, versionport.ErrInvalidManagementPageToken
	}
	return withApplicationVersionManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*versiondomain.OwnedVersionPage, error) {
		if err := r.requireVersionManagementAdmin(tx, adminID, applicationID); err != nil {
			return nil, err
		}
		filter := bson.M{"applicationId": applicationID.String()}
		if reviewStatus != nil {
			filter["reviewStatus"] = string(*reviewStatus)
		}
		if cursor != nil {
			filter["$or"] = bson.A{
				bson.M{"sequence": bson.M{"$lt": cursor.Sequence}},
				bson.M{"sequence": cursor.Sequence, "versionId": bson.M{"$lt": cursor.VersionID}},
			}
		}
		find, err := r.database.Collection(applicationVersionsCollectionName).Find(tx, filter, options.Find().SetSort(bson.D{{Key: "sequence", Value: -1}, {Key: "versionId", Value: -1}}).SetLimit(int64(pageSize)+1))
		if err != nil {
			return nil, versionport.ErrManagementUnavailable
		}
		defer find.Close(tx)
		var documents []applicationVersionDocument
		if err = find.All(tx, &documents); err != nil {
			return nil, versionport.ErrManagementUnavailable
		}
		hasMore := len(documents) > int(pageSize)
		if hasMore {
			documents = documents[:pageSize]
		}
		facts, err := r.loadVersionManagementFacts(tx, applicationID, documents)
		if err != nil {
			return nil, err
		}
		items := make([]versiondomain.OwnedVersion, len(documents))
		for i := range documents {
			items[i], err = projectOwnedVersion(documents[i], facts.oauth[documents[i].VersionID], facts.reviews[documents[i].VersionID])
			if err != nil {
				return nil, versionport.ErrManagementStateInconsistent
			}
		}
		next := ""
		if hasMore && len(documents) != 0 {
			next, err = encodeApplicationVersionManagementCursor(adminID, applicationID, reviewStatus, documents[len(documents)-1])
			if err != nil {
				return nil, versionport.ErrManagementUnavailable
			}
		}
		return &versiondomain.OwnedVersionPage{Items: items, NextPageToken: next, AsOf: asOf}, nil
	})
}

func (r *ApplicationVersionManagementRepository) Get(ctx context.Context, adminID shared.AuthID, applicationID shared.ApplicationID, versionID versiondomain.ApplicationVersionID) (*versiondomain.OwnedVersionDetail, error) {
	if r == nil || r.database == nil || !adminID.IsValid() || !applicationID.IsValid() || !versionID.IsValid() {
		return nil, fmt.Errorf("get application version: invalid input")
	}
	return withApplicationVersionManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*versiondomain.OwnedVersionDetail, error) {
		if err := r.requireVersionManagementAdmin(tx, adminID, applicationID); err != nil {
			return nil, err
		}
		var document applicationVersionDocument
		err := r.database.Collection(applicationVersionsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "versionId": versionID.String()}).Decode(&document)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, versionport.ErrManagementNotFound
		}
		if err != nil {
			return nil, versionport.ErrManagementUnavailable
		}
		facts, err := r.loadVersionManagementFacts(tx, applicationID, []applicationVersionDocument{document})
		if err != nil {
			return nil, err
		}
		version, err := projectOwnedVersion(document, facts.oauth[document.VersionID], facts.reviews[document.VersionID])
		if err != nil {
			return nil, versionport.ErrManagementStateInconsistent
		}
		return &versiondomain.OwnedVersionDetail{Version: version, AsOf: asOf}, nil
	})
}

func withApplicationVersionManagementSnapshot[T any](r *ApplicationVersionManagementRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, versionport.ErrManagementUnavailable
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return fn(tx, time.Now().UTC())
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		if errors.Is(err, versionport.ErrInvalidManagementPageToken) || errors.Is(err, versionport.ErrManagementNotFound) || errors.Is(err, versionport.ErrManagementStateInconsistent) || errors.Is(err, versionport.ErrManagementUnavailable) {
			return zero, err
		}
		return zero, versionport.ErrManagementUnavailable
	}
	value, ok := result.(T)
	if !ok {
		return zero, versionport.ErrManagementUnavailable
	}
	return value, nil
}

func (r *ApplicationVersionManagementRepository) requireVersionManagementAdmin(ctx context.Context, adminID shared.AuthID, applicationID shared.ApplicationID) error {
	var document struct {
		ID      string `bson:"id"`
		AdminID string `bson:"adminId"`
	}
	err := r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": applicationID.String(), "adminId": adminID.String()}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return versionport.ErrManagementNotFound
	}
	if err != nil {
		return versionport.ErrManagementUnavailable
	}
	if document.ID != applicationID.String() || document.AdminID != adminID.String() {
		return versionport.ErrManagementStateInconsistent
	}
	return nil
}

type versionManagementFacts struct {
	oauth   map[string]applicationVersionOAuthConfigDocument
	reviews map[string]*applicationReviewDocument
}

func (r *ApplicationVersionManagementRepository) loadVersionManagementFacts(ctx context.Context, applicationID shared.ApplicationID, versions []applicationVersionDocument) (versionManagementFacts, error) {
	facts := versionManagementFacts{oauth: map[string]applicationVersionOAuthConfigDocument{}, reviews: map[string]*applicationReviewDocument{}}
	if len(versions) == 0 {
		return facts, nil
	}
	ids := make([]string, len(versions))
	for i := range versions {
		if versions[i].ApplicationID != applicationID.String() {
			return facts, versionport.ErrManagementStateInconsistent
		}
		ids[i] = versions[i].VersionID
	}
	oauthCursor, err := r.database.Collection(applicationVersionOAuthConfigsCollectionName).Find(ctx, bson.M{"applicationVersionId": bson.M{"$in": ids}})
	if err != nil {
		return facts, versionport.ErrManagementUnavailable
	}
	var oauthDocuments []applicationVersionOAuthConfigDocument
	if err = oauthCursor.All(ctx, &oauthDocuments); err != nil {
		return facts, versionport.ErrManagementUnavailable
	}
	for _, document := range oauthDocuments {
		if _, duplicate := facts.oauth[document.ApplicationVersionID]; duplicate || document.ApplicationID != applicationID.String() || !slices.Contains(ids, document.ApplicationVersionID) {
			return facts, versionport.ErrManagementStateInconsistent
		}
		facts.oauth[document.ApplicationVersionID] = document
	}
	if len(facts.oauth) != len(ids) {
		return facts, versionport.ErrManagementStateInconsistent
	}
	pipeline := drivermongo.Pipeline{
		{{Key: "$match", Value: bson.M{"versionId": bson.M{"$in": ids}}}},
		{{Key: "$sort", Value: bson.D{{Key: "versionId", Value: 1}, {Key: "attempt", Value: -1}}}},
		{{Key: "$group", Value: bson.M{"_id": "$versionId", "document": bson.M{"$first": "$$ROOT"}}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$document"}}},
	}
	reviewCursor, err := r.database.Collection(applicationReviewsCollectionName).Aggregate(ctx, pipeline)
	if err != nil {
		return facts, versionport.ErrManagementUnavailable
	}
	var reviewDocuments []applicationReviewDocument
	if err = reviewCursor.All(ctx, &reviewDocuments); err != nil {
		return facts, versionport.ErrManagementUnavailable
	}
	for i := range reviewDocuments {
		document := reviewDocuments[i]
		if document.ApplicationID != applicationID.String() || !slices.Contains(ids, document.VersionID) {
			return facts, versionport.ErrManagementStateInconsistent
		}
		copy := document
		facts.reviews[document.VersionID] = &copy
	}
	return facts, nil
}

func projectOwnedVersion(document applicationVersionDocument, oauth applicationVersionOAuthConfigDocument, latest *applicationReviewDocument) (versiondomain.OwnedVersion, error) {
	version, err := applicationVersionFromDocument(document, oauth)
	if err != nil || oauth.ApplicationVersionID == "" {
		return versiondomain.OwnedVersion{}, versionport.ErrManagementStateInconsistent
	}
	projected := versiondomain.OwnedVersion{
		VersionID: version.ID().String(), ApplicationID: version.ApplicationID().String(), Sequence: version.Sequence().Int32(),
		VersionLabel: version.VersionLabel().String(), LaunchURL: version.LaunchURL().String(),
		RPCApiMinVersion: version.RPCApiRange().Minimum(), RPCApiMaxVersionExclusive: version.RPCApiRange().MaximumExclusive(),
		ReviewStatus: string(version.ReviewStatus()), Revision: version.Revision(), CreatedBy: version.CreatedBy().String(), CreatedAt: version.CreatedAt(), UpdatedAt: version.UpdatedAt(),
	}
	for _, capability := range version.RequiredCapabilities() {
		projected.RequiredCapabilities = append(projected.RequiredCapabilities, string(capability))
	}
	for _, scope := range version.RequiredScopes() {
		projected.RequiredScopes = append(projected.RequiredScopes, string(scope))
	}
	for _, scope := range version.OptionalScopes() {
		projected.OptionalScopes = append(projected.OptionalScopes, string(scope))
	}
	redirects := version.OAuthRedirects()
	projected.PKCERedirectURIs = redirects.PKCERedirectURIs()
	projected.ConfidentialRedirectURIs = redirects.ConfidentialRedirectURIs()
	if err := attachOwnedLatestReview(&projected, document, oauth, latest); err != nil {
		return versiondomain.OwnedVersion{}, err
	}
	return projected, nil
}

func attachOwnedLatestReview(projected *versiondomain.OwnedVersion, version applicationVersionDocument, oauth applicationVersionOAuthConfigDocument, document *applicationReviewDocument) error {
	if document == nil {
		if version.ReviewStatus == string(versiondomain.ReviewStatusDraft) {
			return nil
		}
		return versionport.ErrManagementStateInconsistent
	}
	review, err := applicationReviewFromDocument(*document)
	if err != nil || review.ApplicationID().String() != version.ApplicationID || review.VersionID().String() != version.VersionID {
		return versionport.ErrManagementStateInconsistent
	}
	_, _, currentSnapshot, err := applicationVersionDocumentToDecisionVersion(version, oauth)
	if err != nil {
		return versionport.ErrManagementStateInconsistent
	}
	switch version.ReviewStatus {
	case string(versiondomain.ReviewStatusSubmitted):
		if review.Status() != reviewdomain.ReviewStatusPending || review.HasDecision() || review.HasDraftRestoration() || version.Revision != review.SourceVersionRevision()+1 || version.UpdatedBy != review.SubmittedBy().String() || !version.UpdatedAt.Equal(review.SubmittedAt()) || !currentSnapshot.Equal(review.Snapshot()) {
			return versionport.ErrManagementStateInconsistent
		}
	case string(versiondomain.ReviewStatusApproved):
		decision := review.Decision()
		if review.Status() != reviewdomain.ReviewStatusApproved || decision == nil || decision.Outcome() != reviewdomain.ReviewDecisionApproved || review.HasDraftRestoration() || version.Revision != review.SourceVersionRevision()+2 || version.UpdatedBy != decision.DecidedBy().String() || !version.UpdatedAt.Equal(decision.DecidedAt()) || !currentSnapshot.Equal(review.Snapshot()) {
			return versionport.ErrManagementStateInconsistent
		}
	case string(versiondomain.ReviewStatusRejected):
		decision := review.Decision()
		if review.Status() != reviewdomain.ReviewStatusRejected || decision == nil || decision.Outcome() != reviewdomain.ReviewDecisionRejected || review.HasDraftRestoration() || version.Revision != review.SourceVersionRevision()+2 || version.UpdatedBy != decision.DecidedBy().String() || !version.UpdatedAt.Equal(decision.DecidedAt()) || !currentSnapshot.Equal(review.Snapshot()) {
			return versionport.ErrManagementStateInconsistent
		}
	case string(versiondomain.ReviewStatusDraft):
		restoration := review.DraftRestoration()
		if review.Status() != reviewdomain.ReviewStatusRejected || review.Decision() == nil || review.Decision().Outcome() != reviewdomain.ReviewDecisionRejected || restoration == nil || restoration.ResultVersionRevision() != review.SourceVersionRevision()+3 || version.Revision < restoration.ResultVersionRevision() {
			return versionport.ErrManagementStateInconsistent
		}
		if version.Revision == restoration.ResultVersionRevision() && (version.UpdatedBy != restoration.RestoredBy().String() || !version.UpdatedAt.Equal(restoration.RestoredAt())) {
			return versionport.ErrManagementStateInconsistent
		}
	default:
		return versionport.ErrManagementStateInconsistent
	}
	owned := &versiondomain.OwnedLatestReview{ReviewID: review.ReviewID().String(), Attempt: review.Attempt().Int32(), Status: string(review.Status()), SubmittedBy: review.SubmittedBy().String(), SubmittedAt: review.SubmittedAt()}
	if decision := review.Decision(); decision != nil {
		var reason *string
		if decision.Reason() != "" {
			value := decision.Reason()
			reason = &value
		}
		owned.Decision = &versiondomain.OwnedReviewDecision{Outcome: decision.Outcome().String(), Reason: reason, DecidedAt: decision.DecidedAt()}
	}
	if restoration := review.DraftRestoration(); restoration != nil {
		owned.DraftRestoration = &versiondomain.OwnedDraftRestoration{RestoredAt: restoration.RestoredAt()}
	}
	projected.LatestReview = owned
	return nil
}

func versionManagementFingerprint(adminID shared.AuthID, applicationID shared.ApplicationID, reviewStatus *versiondomain.ReviewStatus, sequence int32, versionID string) string {
	status := ""
	if reviewStatus != nil {
		status = string(*reviewStatus)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", adminID.String(), applicationID.String(), status, sequence, versionID)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeApplicationVersionManagementCursor(adminID shared.AuthID, applicationID shared.ApplicationID, reviewStatus *versiondomain.ReviewStatus, last applicationVersionDocument) (string, error) {
	encoded, err := json.Marshal(applicationVersionManagementCursor{Version: 1, Fingerprint: versionManagementFingerprint(adminID, applicationID, reviewStatus, last.Sequence, last.VersionID), Sequence: last.Sequence, VersionID: last.VersionID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeApplicationVersionManagementCursor(value string, adminID shared.AuthID, applicationID shared.ApplicationID, reviewStatus *versiondomain.ReviewStatus) (*applicationVersionManagementCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(raw) > 4096 || base64.RawURLEncoding.EncodeToString(raw) != value {
		return nil, versionport.ErrInvalidManagementPageToken
	}
	var cursor applicationVersionManagementCursor
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.Version != 1 || cursor.Sequence < 1 || !versiondomain.ApplicationVersionID(cursor.VersionID).IsValid() || cursor.Fingerprint != versionManagementFingerprint(adminID, applicationID, reviewStatus, cursor.Sequence, cursor.VersionID) {
		return nil, versionport.ErrInvalidManagementPageToken
	}
	return &cursor, nil
}
