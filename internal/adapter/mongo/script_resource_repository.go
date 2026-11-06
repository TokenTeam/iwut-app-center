package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const (
	scriptResourcesCollectionName      = "script_resources"
	scriptResourceAuditsCollectionName = "script_resource_audits"
	scriptResourceCreated              = "CREATED"
	scriptResourceMetadataReplaced     = "METADATA_REPLACED"
)

type ScriptResourceRepository struct {
	database *drivermongo.Database
}

var _ port.ResourceRepository = (*ScriptResourceRepository)(nil)

func NewScriptResourceRepository(database *drivermongo.Database) *ScriptResourceRepository {
	return &ScriptResourceRepository{database: database}
}

type scriptResourceDocument struct {
	ResourceID string    `bson:"resourceId"`
	Name       string    `bson:"name"`
	Purpose    string    `bson:"purpose"`
	Revision   int64     `bson:"revision"`
	CreatedBy  string    `bson:"createdBy"`
	CreatedAt  time.Time `bson:"createdAt"`
	UpdatedBy  string    `bson:"updatedBy"`
	UpdatedAt  time.Time `bson:"updatedAt"`
}

type scriptResourceAuditDocument struct {
	ResourceID string    `bson:"resourceId"`
	Revision   int64     `bson:"revision"`
	Action     string    `bson:"action"`
	Actor      string    `bson:"actor"`
	ChangedAt  time.Time `bson:"changedAt"`
}

func (doc scriptResourceDocument) resource() domain.Resource {
	return domain.Resource{
		ID:       domain.ResourceID(doc.ResourceID),
		Metadata: domain.Metadata{Name: doc.Name, Purpose: doc.Purpose},
		Revision: doc.Revision, CreatedBy: shared.AuthID(doc.CreatedBy), CreatedAt: doc.CreatedAt.UTC(),
		UpdatedBy: shared.AuthID(doc.UpdatedBy), UpdatedAt: doc.UpdatedAt.UTC(),
	}
}

func (r *ScriptResourceRepository) CreateResource(ctx context.Context, resource domain.Resource) (domain.Resource, error) {
	// Normalize once so both documents and the ACK carry the same BSON time.
	resource.CreatedAt = resource.CreatedAt.UTC().Truncate(time.Millisecond)
	resource.UpdatedAt = resource.UpdatedAt.UTC().Truncate(time.Millisecond)
	if resource.Revision != 1 || resource.Validate() != nil {
		return domain.Resource{}, domain.ErrInternal
	}
	document := scriptResourceDocument{
		ResourceID: string(resource.ID), Name: resource.Metadata.Name, Purpose: resource.Metadata.Purpose,
		Revision: resource.Revision, CreatedBy: string(resource.CreatedBy), CreatedAt: resource.CreatedAt,
		UpdatedBy: string(resource.UpdatedBy), UpdatedAt: resource.UpdatedAt,
	}
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.Resource, error) {
		if _, err := r.database.Collection(scriptResourcesCollectionName).InsertOne(tx, document); err != nil {
			return domain.Resource{}, fmt.Errorf("insert script resource: %w", err)
		}
		if err := r.insertAudit(tx, resource, scriptResourceCreated); err != nil {
			return domain.Resource{}, err
		}
		return resource, nil
	})
}

func (r *ScriptResourceRepository) ReplaceResourceMetadata(ctx context.Context, cmd port.ReplaceMetadata) (domain.Resource, error) {
	at := cmd.At.UTC().Truncate(time.Millisecond)
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.Resource, error) {
		collection := r.database.Collection(scriptResourcesCollectionName)
		filter := bson.D{{Key: "resourceId", Value: string(cmd.ID)}}
		var doc scriptResourceDocument
		err := collection.FindOne(tx, filter).Decode(&doc)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return domain.Resource{}, domain.ErrNotFound
		}
		if err != nil {
			return domain.Resource{}, fmt.Errorf("read script resource: %w", err)
		}
		next, changed, err := doc.resource().Replace(cmd.ExpectedRevision, cmd.Metadata, cmd.Actor, at)
		if err != nil || !changed {
			return next, err
		}
		result, err := collection.UpdateOne(tx,
			append(filter, bson.E{Key: "revision", Value: cmd.ExpectedRevision}),
			bson.D{{Key: "$set", Value: bson.D{
				{Key: "name", Value: next.Metadata.Name},
				{Key: "purpose", Value: next.Metadata.Purpose},
				{Key: "revision", Value: next.Revision},
				{Key: "updatedBy", Value: string(next.UpdatedBy)},
				{Key: "updatedAt", Value: next.UpdatedAt},
			}}})
		if err != nil {
			return domain.Resource{}, fmt.Errorf("replace script resource metadata: %w", err)
		}
		if result.MatchedCount != 1 {
			return domain.Resource{}, domain.ErrRevisionConflict
		}
		if err := r.insertAudit(tx, next, scriptResourceMetadataReplaced); err != nil {
			return domain.Resource{}, err
		}
		return next, nil
	})
}

func (r *ScriptResourceRepository) insertAudit(ctx context.Context, resource domain.Resource, action string) error {
	_, err := r.database.Collection(scriptResourceAuditsCollectionName).InsertOne(ctx, scriptResourceAuditDocument{
		ResourceID: string(resource.ID), Revision: resource.Revision,
		Action: action, Actor: string(resource.UpdatedBy), ChangedAt: resource.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert script resource audit: %w", err)
	}
	return nil
}

func withScriptTransaction[T any](ctx context.Context, database *drivermongo.Database, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	session, err := database.Client().StartSession()
	if err != nil {
		return zero, fmt.Errorf("start script transaction: %w", err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return fn(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return zero, err
	}
	return result.(T), nil
}
