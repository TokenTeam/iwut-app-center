package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const applicationVersionsCollectionName = "application_versions"

type ApplicationVersionRepository struct {
	database *drivermongo.Database
}

var _ versionport.ApplicationVersionRepository = (*ApplicationVersionRepository)(nil)

func NewApplicationVersionRepository(database *drivermongo.Database) *ApplicationVersionRepository {
	return &ApplicationVersionRepository{database: database}
}

func (repository *ApplicationVersionRepository) CreateDraft(
	ctx context.Context,
	expectedAdminID shared.AuthID,
	draft *versiondomain.DraftApplicationVersion,
) (*versiondomain.ApplicationVersion, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("create application version draft: MongoDB database is nil")
	}
	if !expectedAdminID.IsValid() || draft == nil {
		return nil, fmt.Errorf("create application version draft: invalid repository input")
	}
	if expectedAdminID != draft.CreatedBy() {
		return nil, fmt.Errorf("create application version draft: expected administrator does not match created-by identity")
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("start application version creation transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return repository.createDraftTransaction(transactionContext, expectedAdminID, draft)
	}, transactionOptions)
	if err != nil {
		switch {
		case errors.Is(err, versionport.ErrApplicationNotFound):
			return nil, versionport.ErrApplicationNotFound
		case errors.Is(err, versionport.ErrApplicationAdminRequired):
			return nil, versionport.ErrApplicationAdminRequired
		case errors.Is(err, versionport.ErrApplicationVersionLabelAlreadyExists), isApplicationVersionLabelDuplicate(err):
			return nil, versionport.ErrApplicationVersionLabelAlreadyExists
		default:
			return nil, fmt.Errorf("create application version transaction: %w", err)
		}
	}

	version, ok := result.(*versiondomain.ApplicationVersion)
	if !ok || version == nil {
		return nil, fmt.Errorf("create application version transaction: invalid transaction result")
	}
	return version, nil
}

func (repository *ApplicationVersionRepository) ReplaceDraft(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID versiondomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
	replacement versiondomain.DraftApplicationVersionReplacement,
	updatedAt time.Time,
) (*versiondomain.ApplicationVersion, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("replace application version draft: MongoDB database is nil")
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !expectedAdminID.IsValid() ||
		expectedRevision < 1 || updatedAt.IsZero() {
		return nil, fmt.Errorf("replace application version draft: invalid repository input")
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("start application version update transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())
	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return repository.replaceDraftTransaction(
			transactionContext,
			applicationID,
			versionID,
			expectedAdminID,
			expectedRevision,
			replacement,
			updatedAt.UTC(),
		)
	}, transactionOptions)
	if err != nil {
		switch {
		case errors.Is(err, versionport.ErrApplicationVersionNotFound):
			return nil, versionport.ErrApplicationVersionNotFound
		case errors.Is(err, versionport.ErrApplicationAdminRequired):
			return nil, versionport.ErrApplicationAdminRequired
		case errors.Is(err, versionport.ErrApplicationVersionNotDraft):
			return nil, versionport.ErrApplicationVersionNotDraft
		case errors.Is(err, versionport.ErrApplicationVersionRevisionConflict):
			return nil, versionport.ErrApplicationVersionRevisionConflict
		case errors.Is(err, versionport.ErrApplicationVersionLabelAlreadyExists), isApplicationVersionLabelDuplicate(err):
			return nil, versionport.ErrApplicationVersionLabelAlreadyExists
		default:
			return nil, fmt.Errorf("replace application version transaction: %w", err)
		}
	}

	version, ok := result.(*versiondomain.ApplicationVersion)
	if !ok || version == nil {
		return nil, fmt.Errorf("replace application version transaction: invalid transaction result")
	}
	return version, nil
}

func (repository *ApplicationVersionRepository) replaceDraftTransaction(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID versiondomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
	replacement versiondomain.DraftApplicationVersionReplacement,
	updatedAt time.Time,
) (*versiondomain.ApplicationVersion, error) {
	applications := repository.database.Collection(applicationsCollectionName)
	versions := repository.database.Collection(applicationVersionsCollectionName)

	// FindOneAndUpdate establishes a write conflict with an administrator
	// transfer while atomically rechecking the current administrator. Setting the
	// existing value leaves the Application's business state unchanged.
	var ownedApplication struct {
		ID string `bson:"id"`
	}
	err := applications.FindOneAndUpdate(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}, {Key: "adminId", Value: expectedAdminID.String()}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: expectedAdminID.String()}}}},
		options.FindOneAndUpdate().SetProjection(bson.D{{Key: "id", Value: 1}}),
	).Decode(&ownedApplication)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		var existing struct {
			ID string `bson:"id"`
		}
		err = applications.FindOne(
			ctx,
			bson.D{{Key: "id", Value: applicationID.String()}},
			options.FindOne().SetProjection(bson.D{{Key: "id", Value: 1}}),
		).Decode(&existing)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			// The update UC intentionally hides whether a version exists when its
			// path Application does not.
			return nil, versionport.ErrApplicationVersionNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("classify application version ownership: %w", err)
		}
		return nil, versionport.ErrApplicationAdminRequired
	}
	if err != nil {
		return nil, fmt.Errorf("lock application version ownership: %w", err)
	}

	var currentDocument applicationVersionDocument
	err = versions.FindOne(ctx, bson.D{
		{Key: "versionId", Value: versionID.String()},
		{Key: "applicationId", Value: applicationID.String()},
	}).Decode(&currentDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, versionport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read application version draft: %w", err)
	}
	current, err := applicationVersionFromDocument(currentDocument)
	if err != nil {
		return nil, err
	}
	if current.ReviewStatus() != versiondomain.ReviewStatusDraft {
		return nil, versionport.ErrApplicationVersionNotDraft
	}
	if current.Revision() != expectedRevision {
		return nil, versionport.ErrApplicationVersionRevisionConflict
	}

	updated, err := current.ReplaceDraft(expectedRevision, replacement, expectedAdminID, updatedAt)
	if err != nil {
		switch {
		case errors.Is(err, versiondomain.ErrApplicationVersionNotDraft):
			return nil, versionport.ErrApplicationVersionNotDraft
		case errors.Is(err, versiondomain.ErrApplicationVersionRevisionConflict):
			return nil, versionport.ErrApplicationVersionRevisionConflict
		default:
			return nil, fmt.Errorf("replace application version domain state: %w", err)
		}
	}
	if updated.Revision() == current.Revision() {
		return updated, nil
	}

	var updatedDocument applicationVersionDocument
	err = versions.FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "versionId", Value: versionID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "reviewStatus", Value: string(versiondomain.ReviewStatusDraft)},
			{Key: "revision", Value: expectedRevision},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "versionLabel", Value: updated.VersionLabel().String()},
				{Key: "launchUrl", Value: updated.LaunchURL().String()},
				{Key: "rpcApiMinVersion", Value: updated.RPCApiRange().Minimum()},
				{Key: "rpcApiMaxVersionExclusive", Value: updated.RPCApiRange().MaximumExclusive()},
				{Key: "requiredCapabilities", Value: capabilityNamesToStrings(updated.RequiredCapabilities())},
				{Key: "requiredScopes", Value: scopeNamesToStrings(updated.RequiredScopes())},
				{Key: "optionalScopes", Value: scopeNamesToStrings(updated.OptionalScopes())},
				{Key: "updatedBy", Value: updated.UpdatedBy().String()},
				{Key: "updatedAt", Value: updated.UpdatedAt()},
			}},
			{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, versionport.ErrApplicationVersionRevisionConflict
	}
	if isApplicationVersionLabelDuplicate(err) {
		return nil, versionport.ErrApplicationVersionLabelAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("update application version draft: %w", err)
	}
	result, err := applicationVersionFromDocument(updatedDocument)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (repository *ApplicationVersionRepository) createDraftTransaction(
	ctx context.Context,
	expectedAdminID shared.AuthID,
	draft *versiondomain.DraftApplicationVersion,
) (*versiondomain.ApplicationVersion, error) {
	applications := repository.database.Collection(applicationsCollectionName)
	versions := repository.database.Collection(applicationVersionsCollectionName)

	var allocation struct {
		NextVersionSequence int32 `bson:"nextVersionSequence"`
	}
	err := applications.FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "id", Value: draft.ApplicationID().String()},
			{Key: "adminId", Value: expectedAdminID.String()},
		},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "nextVersionSequence", Value: int32(1)}}}},
		options.FindOneAndUpdate().
			SetProjection(bson.D{{Key: "nextVersionSequence", Value: 1}}).
			SetReturnDocument(options.Before),
	).Decode(&allocation)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		var existing struct {
			AdminID string `bson:"adminId"`
		}
		err = applications.FindOne(
			ctx,
			bson.D{{Key: "id", Value: draft.ApplicationID().String()}},
			options.FindOne().SetProjection(bson.D{{Key: "adminId", Value: 1}}),
		).Decode(&existing)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, versionport.ErrApplicationNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("classify application ownership: %w", err)
		}
		return nil, versionport.ErrApplicationAdminRequired
	}
	if err != nil {
		return nil, fmt.Errorf("allocate application version sequence: %w", err)
	}

	sequence, err := versiondomain.NewVersionSequence(allocation.NextVersionSequence)
	if err != nil {
		return nil, fmt.Errorf("allocate application version sequence: %w", errCorruptApplicationDocument)
	}
	document, err := applicationVersionToDocument(draft, sequence)
	if err != nil {
		return nil, err
	}
	if _, err := versions.InsertOne(ctx, document); err != nil {
		if isApplicationVersionLabelDuplicate(err) {
			return nil, versionport.ErrApplicationVersionLabelAlreadyExists
		}
		return nil, fmt.Errorf("insert application version: %w", err)
	}

	version, err := applicationVersionFromDocument(document)
	if err != nil {
		return nil, err
	}
	return version, nil
}

func isApplicationVersionLabelDuplicate(err error) bool {
	if !drivermongo.IsDuplicateKeyError(err) {
		return false
	}

	var writeException drivermongo.WriteException
	if errors.As(err, &writeException) {
		for _, writeError := range writeException.WriteErrors {
			if keyPatternIsApplicationVersionLabel(writeError.Details) ||
				strings.Contains(writeError.Message, applicationVersionLabelUniqueIndexName) {
				return true
			}
		}
	}

	var commandError drivermongo.CommandError
	return errors.As(err, &commandError) &&
		(keyPatternIsApplicationVersionLabel(commandError.Raw) ||
			strings.Contains(commandError.Message, applicationVersionLabelUniqueIndexName))
}

func keyPatternIsApplicationVersionLabel(raw bson.Raw) bool {
	if len(raw) == 0 {
		return false
	}
	pattern, ok := raw.Lookup("keyPattern").DocumentOK()
	if !ok {
		return false
	}
	elements, err := pattern.Elements()
	return err == nil && len(elements) == 2 &&
		elements[0].Key() == "applicationId" && elements[1].Key() == "versionLabel"
}
