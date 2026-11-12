package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const (
	scriptVersionsCollection      = "script_versions"
	scriptVersionAuditsCollection = "script_version_audits"
	scriptDraftCreated            = "DRAFT_CREATED"
	scriptDraftReplaced           = "DRAFT_REPLACED"
)

type ScriptVersionRepository struct {
	database *drivermongo.Database
}

var _ port.DraftRepository = (*ScriptVersionRepository)(nil)

func NewScriptVersionRepository(database *drivermongo.Database) *ScriptVersionRepository {
	return &ScriptVersionRepository{database: database}
}

type scriptVersionDocument struct {
	DependencyFence *int64              `bson:"dependencyFence,omitempty"`
	ResourceID      domain.ResourceID   `bson:"resourceId"`
	VersionID       domain.VersionID    `bson:"versionId"`
	VersionLabel    string              `bson:"versionLabel"`
	Format          string              `bson:"contentFormat"`
	Text            string              `bson:"contentText"`
	Digest          string              `bson:"digest"`
	State           domain.VersionState `bson:"state"`
	Revision        int64               `bson:"revision"`
	Contributors    []shared.AuthID     `bson:"contributors"`
	CreatedBy       shared.AuthID       `bson:"createdBy"`
	CreatedAt       time.Time           `bson:"createdAt"`
	UpdatedBy       shared.AuthID       `bson:"updatedBy"`
	UpdatedAt       time.Time           `bson:"updatedAt"`
}

func toScriptVersionDocument(v domain.Version) scriptVersionDocument {
	return scriptVersionDocument{
		ResourceID: v.ResourceID, VersionID: v.ID, VersionLabel: v.Label,
		Format: v.Content.Format, Text: v.Content.Text, Digest: v.Content.Digest,
		State: v.State, Revision: v.Revision, Contributors: v.Contributors,
		CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, UpdatedBy: v.UpdatedBy, UpdatedAt: v.UpdatedAt,
	}
}

func (d scriptVersionDocument) version() domain.Version {
	return domain.Version{
		ResourceID: d.ResourceID, ID: d.VersionID, Label: d.VersionLabel,
		Content: domain.Content{Format: d.Format, Text: d.Text, Digest: d.Digest},
		State:   d.State, Revision: d.Revision, Contributors: d.Contributors,
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt.UTC(), UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt.UTC(),
	}
}

type scriptVersionAuditDocument struct {
	ResourceID domain.ResourceID `bson:"resourceId"`
	VersionID  domain.VersionID  `bson:"versionId"`
	Revision   int64             `bson:"revision"`
	Digest     string            `bson:"digest"`
	Action     string            `bson:"action"`
	Actor      shared.AuthID     `bson:"actor"`
	ChangedAt  time.Time         `bson:"changedAt"`
}

func (r *ScriptVersionRepository) CreateDraft(ctx context.Context, draft domain.Version) (domain.Version, error) {
	draft.CreatedAt = draft.CreatedAt.UTC().Truncate(time.Millisecond)
	draft.UpdatedAt = draft.UpdatedAt.UTC().Truncate(time.Millisecond)
	if draft.State != domain.VersionDraft || draft.Revision != 1 || draft.Validate() != nil {
		return domain.Version{}, domain.ErrInternal
	}
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.Version, error) {
		if _, err := requireScriptOwnerWritable(tx, r.database, draft.ResourceID, draft.CreatedBy); err != nil {
			return domain.Version{}, err
		}
		if _, err := r.database.Collection(scriptVersionsCollection).InsertOne(tx, toScriptVersionDocument(draft)); err != nil {
			if drivermongo.IsDuplicateKeyError(err) && strings.Contains(err.Error(), scriptVersionLabelIndex) {
				return domain.Version{}, domain.ErrVersionLabelConflict
			}
			return domain.Version{}, fmt.Errorf("insert script draft: %w", err)
		}
		if err := r.insertAudit(tx, draft, scriptDraftCreated); err != nil {
			return domain.Version{}, err
		}
		return draft, nil
	})
}

func (r *ScriptVersionRepository) ReplaceDraft(ctx context.Context, cmd port.ReplaceDraft) (domain.Version, error) {
	at := cmd.At.UTC().Truncate(time.Millisecond)
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.Version, error) {
		if _, err := requireScriptOwnerWritable(tx, r.database, cmd.ResourceID, cmd.Actor); err != nil {
			return domain.Version{}, err
		}
		current, err := r.loadVersion(tx, cmd.ResourceID, cmd.VersionID)
		if err != nil {
			return domain.Version{}, err
		}
		if !current.AuthoredBy(cmd.Actor) {
			return domain.Version{}, domain.ErrInternal
		}
		next, changed, err := current.ReplaceDraft(cmd.ExpectedRevision, cmd.Content, cmd.Actor, at)
		if err != nil || !changed {
			return next, err
		}
		if err := r.writeDraftTransition(tx, current, bson.D{
			{Key: "contentFormat", Value: next.Content.Format},
			{Key: "contentText", Value: next.Content.Text},
			{Key: "digest", Value: next.Content.Digest},
			{Key: "contributors", Value: next.Contributors},
			{Key: "revision", Value: next.Revision},
			{Key: "updatedBy", Value: next.UpdatedBy},
			{Key: "updatedAt", Value: next.UpdatedAt},
		}); err != nil {
			return domain.Version{}, err
		}
		if err := r.insertAudit(tx, next, scriptDraftReplaced); err != nil {
			return domain.Version{}, err
		}
		return next, nil
	})
}

func requireScriptOwnerWritable(ctx context.Context, db *drivermongo.Database, id domain.ResourceID, actor shared.AuthID) (domain.Resource, error) {
	if err := requireOwnerWritable(ctx, db, actor.String(), false); err != nil {
		return domain.Resource{}, err
	}
	return loadOwnedScriptResource(ctx, db, id, actor)
}

func loadOwnedScriptResource(ctx context.Context, db *drivermongo.Database, id domain.ResourceID, actor shared.AuthID) (domain.Resource, error) {
	return findScriptResource(ctx, db, bson.D{{Key: "resourceId", Value: id}, {Key: "ownerAuthId", Value: actor}})
}

func loadScriptResource(ctx context.Context, db *drivermongo.Database, id domain.ResourceID) (domain.Resource, error) {
	return findScriptResource(ctx, db, bson.D{{Key: "resourceId", Value: id}})
}

func findScriptResource(ctx context.Context, db *drivermongo.Database, filter bson.D) (domain.Resource, error) {
	var doc scriptResourceDocument
	err := db.Collection(scriptResourcesCollectionName).FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return domain.Resource{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Resource{}, fmt.Errorf("read script resource: %w", err)
	}
	resource := doc.resource()
	if err := resource.Validate(); err != nil {
		return domain.Resource{}, err
	}
	return resource, nil
}

func (r *ScriptVersionRepository) loadVersion(ctx context.Context, resourceID domain.ResourceID, id domain.VersionID) (domain.Version, error) {
	var doc scriptVersionDocument
	err := r.database.Collection(scriptVersionsCollection).FindOne(ctx,
		bson.D{{Key: "resourceId", Value: resourceID}, {Key: "versionId", Value: id}}).Decode(&doc)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return domain.Version{}, domain.ErrVersionNotFound
	}
	if err != nil {
		return domain.Version{}, fmt.Errorf("read script version: %w", err)
	}
	return doc.version(), nil
}

func (r *ScriptVersionRepository) writeDraftTransition(ctx context.Context, current domain.Version, update bson.D) error {
	result, err := r.database.Collection(scriptVersionsCollection).UpdateOne(ctx, bson.D{
		{Key: "resourceId", Value: current.ResourceID}, {Key: "versionId", Value: current.ID},
		{Key: "state", Value: domain.VersionDraft}, {Key: "revision", Value: current.Revision},
	}, bson.D{{Key: "$set", Value: update}})
	if err != nil {
		return fmt.Errorf("update script draft: %w", err)
	}
	if result.MatchedCount != 1 {
		return domain.ErrRevisionConflict
	}
	return nil
}

func (r *ScriptVersionRepository) insertAudit(ctx context.Context, v domain.Version, action string) error {
	_, err := r.database.Collection(scriptVersionAuditsCollection).InsertOne(ctx, scriptVersionAuditDocument{
		ResourceID: v.ResourceID, VersionID: v.ID, Revision: v.Revision, Digest: v.Content.Digest,
		Action: action, Actor: v.UpdatedBy, ChangedAt: v.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert script version audit: %w", err)
	}
	return nil
}
