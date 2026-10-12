package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

var _ publicationport.TestPublicationClearRepository = (*ApplicationPublicationRepository)(nil)

func (r *ApplicationPublicationRepository) ClearTest(ctx context.Context, appID shared.ApplicationID, major int32, admin shared.AuthID, expected int64, audit publicationport.TestClearAuditFactory) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || !appID.IsValid() || major < 1 || !admin.IsValid() || expected < 1 || audit == nil {
		return nil, fmt.Errorf("clear test publication: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var app applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.D{{Key: "id", Value: appID.String()}}).Decode(&app)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, publicationport.ErrApplicationPublicationNotFound
		}
		if err != nil {
			return nil, err
		}
		if app.AdminID != admin.String() {
			return nil, publicationport.ErrApplicationAdminRequired
		}
		switch app.LifecycleStatus {
		case "CLOSING", "CLOSED":
			return nil, publicationport.ErrApplicationPublicationNotAllowed
		case "ACTIVE":
		default:
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		raw, err := r.database.Collection(applicationPublicationsCollectionName).FindOne(tx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "rpcApiMajor", Value: major}}).Raw()
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, publicationport.ErrApplicationPublicationNotFound
		}
		if err != nil {
			return nil, err
		}
		publication, err := restoreStrictPublication(raw)
		if err != nil || publication.ApplicationID() != appID || publication.RPCAPIMajor() != major {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		candidate, err := publicationdomain.NewTestClearCandidate(publication, expected)
		if err != nil {
			return nil, mapGreyDomainError(err)
		}
		if candidate.IsNoOp() {
			return publicationdomain.NewNoOpResult(publication)
		}
		if app.CoordinationRevision < 0 || app.CoordinationRevision == math.MaxInt64 || publication.Revision() == math.MaxInt64 {
			return nil, fmt.Errorf("clear test publication: revision overflow")
		}
		filter := bson.D{{Key: "id", Value: appID.String()}, {Key: "adminId", Value: admin.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}, {Key: "coordinationRevision", Value: app.CoordinationRevision}}
		locked, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx, filter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}})
		if err != nil {
			return nil, err
		}
		if locked.MatchedCount != 1 {
			return nil, publicationport.ErrApplicationAdminRequired
		}
		id, at, err := audit()
		if err != nil {
			return nil, err
		}
		changed, err := candidate.Clear(id, admin, at.UTC().Truncate(time.Millisecond))
		if err != nil {
			return nil, err
		}
		if err := r.persistPublicationChange(tx, publication, changed); err != nil {
			return nil, err
		}
		return changed, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("clear test publication transaction: %w", err)
	}
	result, ok := value.(*publicationdomain.PlaceInTestResult)
	if !ok || result == nil {
		return nil, fmt.Errorf("clear test publication: invalid result")
	}
	return result, nil
}

func restoreStrictPublication(raw bson.Raw) (*publicationdomain.ApplicationPublication, error) {
	required := []string{"publicationId", "applicationId", "rpcApiMajor", "testVersionId", "stableVersionId", "greyRollout", "revision", "createdBy", "createdAt", "updatedBy", "updatedAt"}
	allowed := map[string]bool{"_id": true}
	types := map[string]bson.Type{"publicationId": bson.TypeString, "applicationId": bson.TypeString, "rpcApiMajor": bson.TypeInt32, "revision": bson.TypeInt64, "createdBy": bson.TypeString, "createdAt": bson.TypeDateTime, "updatedBy": bson.TypeString, "updatedAt": bson.TypeDateTime}
	for key, expectedType := range types {
		if raw.Lookup(key).Type != expectedType {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
	}
	for _, key := range []string{"testVersionId", "stableVersionId"} {
		if kind := raw.Lookup(key).Type; kind != bson.TypeString && kind != bson.TypeNull {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
	}
	if kind := raw.Lookup("greyRollout").Type; kind != bson.TypeEmbeddedDocument && kind != bson.TypeNull {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	for _, field := range required {
		allowed[field] = true
		if _, err := raw.LookupErr(field); err != nil {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
	}
	elements, err := raw.Elements()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, element := range elements {
		if !allowed[element.Key()] || seen[element.Key()] {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		seen[element.Key()] = true
	}
	var doc applicationPublicationDocument
	if grey := raw.Lookup("greyRollout"); grey.Type == bson.TypeEmbeddedDocument {
		fields := map[string]bool{"rolloutId": true, "versionId": true, "exposureBasisPoints": true, "cohortSeed": true}
		elements, err := grey.Document().Elements()
		if err != nil || len(elements) != len(fields) {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		for _, element := range elements {
			if !fields[element.Key()] {
				return nil, publicationport.ErrApplicationPublicationStateInconsistent
			}
			delete(fields, element.Key())
		}
		if len(fields) != 0 {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		for key, kind := range map[string]bson.Type{"rolloutId": bson.TypeString, "versionId": bson.TypeString, "exposureBasisPoints": bson.TypeInt32, "cohortSeed": bson.TypeBinary} {
			if grey.Document().Lookup(key).Type != kind {
				return nil, publicationport.ErrApplicationPublicationStateInconsistent
			}
		}
	}
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Revision == 1 && (doc.CreatedBy != doc.UpdatedBy || !doc.CreatedAt.Equal(doc.UpdatedAt)) {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	return applicationPublicationFromDocument(doc)
}
