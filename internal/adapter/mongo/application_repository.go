package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	applicationsCollectionName              = "applications_v2"
	applicationCreationQuotasCollectionName = "application_creation_quotas"
)

var errQuotaExhausted = errors.New("application creation quota exhausted")

// ApplicationRepository persists Application creation as one MongoDB
// transaction. Schema changes are intentionally owned by Migrator, not this
// constructor or the normal repository path.
type ApplicationRepository struct {
	database *drivermongo.Database
}

var _ port.ApplicationRepository = (*ApplicationRepository)(nil)

func NewApplicationRepository(database *drivermongo.Database) *ApplicationRepository {
	return &ApplicationRepository{database: database}
}

func (repository *ApplicationRepository) CreateWithinQuota(
	ctx context.Context,
	application *domain.Application,
	initialLimit int32,
) error {
	if repository == nil || repository.database == nil {
		return fmt.Errorf("create application within quota: MongoDB database is nil")
	}
	if initialLimit < 0 {
		return fmt.Errorf("create application within quota: initial limit must be non-negative")
	}

	document, err := applicationToDocument(application)
	if err != nil {
		return fmt.Errorf("create application within quota: %w", err)
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return fmt.Errorf("start application creation transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return nil, repository.createWithinQuotaTransaction(transactionContext, document, initialLimit)
	}, transactionOptions)
	if err == nil {
		return nil
	}
	if errors.Is(err, errQuotaExhausted) {
		return port.ErrApplicationQuotaExceeded
	}
	if errors.Is(err, port.ErrApplicationNameAlreadyExists) {
		return port.ErrApplicationNameAlreadyExists
	}
	if isAdminNameDuplicate(err) {
		return port.ErrApplicationNameAlreadyExists
	}
	return fmt.Errorf("create application transaction: %w", err)
}

func (repository *ApplicationRepository) createWithinQuotaTransaction(
	ctx context.Context,
	document applicationDocument,
	initialLimit int32,
) error {
	applications := repository.database.Collection(applicationsCollectionName)
	quotas := repository.database.Collection(applicationCreationQuotasCollectionName)

	nameCount, err := applications.CountDocuments(ctx, bson.D{
		{Key: "adminId", Value: document.AdminID},
		{Key: "nameKey", Value: document.NameKey},
	}, options.Count().SetLimit(1))
	if err != nil {
		return fmt.Errorf("check application name occupancy: %w", err)
	}
	if nameCount != 0 {
		return port.ErrApplicationNameAlreadyExists
	}

	initialQuota := applicationCreationQuotaDocument{
		AdminID:   document.AdminID,
		Limit:     initialLimit,
		UsedCount: 0,
		Revision:  0,
		UpdatedAt: document.CreatedAt,
	}
	_, err = quotas.UpdateOne(
		ctx,
		bson.D{{Key: "adminId", Value: document.AdminID}},
		bson.D{{Key: "$setOnInsert", Value: initialQuota}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("ensure application creation quota: %w", err)
	}

	quotaResult, err := quotas.UpdateOne(
		ctx,
		bson.D{
			{Key: "adminId", Value: document.AdminID},
			{Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{"$usedCount", "$limit"}}}},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "updatedAt", Value: document.CreatedAt},
			}},
			{Key: "$inc", Value: bson.D{
				{Key: "usedCount", Value: int32(1)},
				{Key: "revision", Value: int64(1)},
			}},
		},
	)
	if err != nil {
		return fmt.Errorf("consume application creation quota: %w", err)
	}
	if quotaResult.MatchedCount == 0 {
		return errQuotaExhausted
	}

	if _, err := applications.InsertOne(ctx, document); err != nil {
		return fmt.Errorf("insert application: %w", err)
	}
	return nil
}

func isAdminNameDuplicate(err error) bool {
	if !drivermongo.IsDuplicateKeyError(err) {
		return false
	}

	var writeException drivermongo.WriteException
	if errors.As(err, &writeException) {
		for _, writeError := range writeException.WriteErrors {
			if keyPatternIsAdminName(writeError.Details) ||
				strings.Contains(writeError.Message, applicationAdminNameUniqueIndexName) {
				return true
			}
		}
	}

	var commandError drivermongo.CommandError
	return errors.As(err, &commandError) &&
		(keyPatternIsAdminName(commandError.Raw) || strings.Contains(commandError.Message, applicationAdminNameUniqueIndexName))
}

func keyPatternIsAdminName(raw bson.Raw) bool {
	if len(raw) == 0 {
		return false
	}
	patternValue := raw.Lookup("keyPattern")
	pattern, ok := patternValue.DocumentOK()
	if !ok {
		return false
	}
	elements, err := pattern.Elements()
	if err != nil || len(elements) != 2 {
		return false
	}
	return elements[0].Key() == "adminId" && elements[1].Key() == "nameKey"
}
