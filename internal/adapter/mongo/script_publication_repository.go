package mongo

import (
	"context"
	"errors"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"

	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const (
	scriptPublicationsCollection      = "script_publications"
	scriptAvailabilityCollection      = "script_distribution_availability"
	scriptPublishedVersionsCollection = "script_published_versions"
	scriptPublicationEventsCollection = "script_publication_events"
)

type scriptPublicationDocument struct {
	ResourceID string    `bson:"resourceId"`
	Head       *string   `bson:"head"`
	Revision   int64     `bson:"revision"`
	UpdatedBy  string    `bson:"updatedBy"`
	UpdatedAt  time.Time `bson:"updatedAt"`
}

type scriptAvailabilityDocument struct {
	ResourceID           string    `bson:"resourceId"`
	State                string    `bson:"state"`
	Revision             int64     `bson:"revision"`
	CoordinationRevision int64     `bson:"coordinationRevision"`
	UpdatedBy            string    `bson:"updatedBy"`
	UpdatedAt            time.Time `bson:"updatedAt"`
}

type scriptPublishedVersionDocument struct {
	VersionID  string `bson:"versionId"`
	ReviewID   string `bson:"reviewId"`
	DecisionID string `bson:"decisionId"`
	Label      string `bson:"versionLabel"`
	Format     string `bson:"format"`
	Digest     string `bson:"digest"`
	ByteLength int64  `bson:"byteLength"`
}

func scriptPublishedVersion(v domain.PublishedScript) scriptPublishedVersionDocument {
	return scriptPublishedVersionDocument{VersionID: string(v.VersionID), ReviewID: string(v.ReviewID), DecisionID: v.DecisionID,
		Label: v.Label, Format: v.Format, Digest: v.Digest, ByteLength: v.ByteLength}
}

func (d scriptPublishedVersionDocument) version() domain.PublishedScript {
	return domain.PublishedScript{VersionID: domain.VersionID(d.VersionID), ReviewID: domain.ReviewID(d.ReviewID), DecisionID: d.DecisionID,
		Label: d.Label, Format: d.Format, Digest: d.Digest, ByteLength: d.ByteLength}
}

type scriptPublishedFactDocument struct {
	ResourceID   string                         `bson:"resourceId"`
	Version      scriptPublishedVersionDocument `bson:"version"`
	FirstEventID string                         `bson:"firstEventId"`
	PublishedBy  string                         `bson:"publishedBy"`
	PublishedAt  time.Time                      `bson:"publishedAt"`
}

type scriptPublicationEventDocument struct {
	EventID    string                          `bson:"eventId"`
	ResourceID string                          `bson:"resourceId"`
	Namespace  string                          `bson:"namespace"`
	Action     string                          `bson:"action"`
	Revision   int64                           `bson:"revision"`
	Version    *scriptPublishedVersionDocument `bson:"version"`
	Actor      string                          `bson:"actor"`
	At         time.Time                       `bson:"at"`
	Reason     string                          `bson:"reason"`
}

type ScriptPublicationRepository struct{ db *dm.Database }

func NewScriptPublicationRepository(db *dm.Database) *ScriptPublicationRepository {
	return &ScriptPublicationRepository{db: db}
}

var _ port.PublicationRepository = (*ScriptPublicationRepository)(nil)

func initializeScriptPublication(ctx context.Context, db *dm.Database, resource domain.Resource) error {
	state := domain.InitialPublication(resource)
	if _, err := db.Collection(scriptPublicationsCollection).InsertOne(ctx, scriptPublicationDocument{
		ResourceID: string(resource.ID), Revision: state.PublicationRevision, UpdatedBy: resource.CreatedBy.String(), UpdatedAt: resource.CreatedAt,
	}); err != nil {
		return err
	}
	_, err := db.Collection(scriptAvailabilityCollection).InsertOne(ctx, scriptAvailabilityDocument{
		ResourceID: string(resource.ID), State: state.Availability, Revision: state.AvailabilityRevision,
		CoordinationRevision: 1, UpdatedBy: resource.CreatedBy.String(), UpdatedAt: resource.CreatedAt,
	})
	return err
}

func (r *ScriptPublicationRepository) approvedVersion(ctx context.Context, resourceID domain.ResourceID, versionID domain.VersionID) (domain.PublishedScript, time.Time, error) {
	loaded, err := loadScriptDependency(ctx, r.db, shared.ScriptReference{ResourceID: string(resourceID), VersionID: string(versionID)})
	if errors.Is(err, shared.ErrInvalidScriptReferences) {
		return domain.PublishedScript{}, time.Time{}, domain.ErrVersionNotFound
	}
	if errors.Is(err, reviewport.ErrApplicationReviewDataInconsistent) || errors.Is(err, port.ErrScriptQueryInconsistent) {
		return domain.PublishedScript{}, time.Time{}, domain.ErrPublicationInconsistent
	}
	if err != nil {
		return domain.PublishedScript{}, time.Time{}, err
	}
	if loaded.version.State != domain.VersionApproved {
		return domain.PublishedScript{}, time.Time{}, domain.ErrScriptNotApproved
	}
	v := domain.PublishedScript{VersionID: versionID, ReviewID: loaded.review.ReviewID, DecisionID: loaded.review.Decision.ID,
		Label: loaded.version.VersionLabel, Format: loaded.version.Format, Digest: loaded.version.Digest, ByteLength: int64(len(loaded.version.Text))}
	return v, loaded.review.Decision.At, v.Validate()
}

func (r *ScriptPublicationRepository) publishedFact(ctx context.Context, resourceID domain.ResourceID, version domain.PublishedScript) (*scriptPublishedFactDocument, error) {
	var fact scriptPublishedFactDocument
	err := r.db.Collection(scriptPublishedVersionsCollection).FindOne(ctx,
		bson.M{"resourceId": resourceID, "version.versionId": version.VersionID}).Decode(&fact)
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fact.Version != scriptPublishedVersion(version) || !canonicalScriptID(fact.FirstEventID) ||
		!shared.AuthID(fact.PublishedBy).IsValid() || !validScriptQueryTime(fact.PublishedAt) {
		return nil, domain.ErrPublicationInconsistent
	}
	var first scriptPublicationEventDocument
	err = r.db.Collection(scriptPublicationEventsCollection).FindOne(ctx, bson.M{"eventId": fact.FirstEventID}).Decode(&first)
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, domain.ErrPublicationInconsistent
	}
	if err != nil {
		return nil, err
	}
	if first.ResourceID != string(resourceID) || first.Namespace != "PUBLICATION" || first.Action != domain.PublishScript ||
		first.Version == nil || *first.Version != fact.Version || first.Revision < 2 || first.Actor != fact.PublishedBy ||
		!first.At.Equal(fact.PublishedAt) || first.Reason != "" {
		return nil, domain.ErrPublicationInconsistent
	}
	return &fact, nil
}

func (r *ScriptPublicationRepository) loadState(ctx context.Context, resourceID domain.ResourceID) (domain.PublicationState, scriptAvailabilityDocument, error) {
	var state domain.PublicationState
	var gate scriptAvailabilityDocument
	if _, err := loadScriptResource(ctx, r.db, resourceID); err != nil {
		return state, gate, err
	}
	var pub scriptPublicationDocument
	err := r.db.Collection(scriptPublicationsCollection).FindOne(ctx, bson.M{"resourceId": resourceID}).Decode(&pub)
	if errors.Is(err, dm.ErrNoDocuments) {
		return state, gate, domain.ErrPublicationInconsistent
	}
	if err != nil {
		return state, gate, err
	}
	err = r.db.Collection(scriptAvailabilityCollection).FindOne(ctx, bson.M{"resourceId": resourceID}).Decode(&gate)
	if errors.Is(err, dm.ErrNoDocuments) {
		return state, gate, domain.ErrPublicationInconsistent
	}
	if err != nil {
		return state, gate, err
	}
	state = domain.PublicationState{ResourceID: resourceID, PublicationRevision: pub.Revision,
		Availability: gate.State, AvailabilityRevision: gate.Revision, PublicationUpdatedBy: shared.AuthID(pub.UpdatedBy),
		PublicationUpdatedAt: pub.UpdatedAt.UTC(), AvailabilityUpdatedBy: shared.AuthID(gate.UpdatedBy), AvailabilityUpdatedAt: gate.UpdatedAt.UTC()}
	if gate.CoordinationRevision < 1 {
		return state, gate, domain.ErrPublicationInconsistent
	}
	if pub.Head != nil {
		version, approvedAt, err := r.approvedVersion(ctx, resourceID, domain.VersionID(*pub.Head))
		if errors.Is(err, domain.ErrVersionNotFound) || errors.Is(err, domain.ErrScriptNotApproved) {
			return state, gate, domain.ErrPublicationInconsistent
		}
		if err != nil {
			return state, gate, err
		}
		fact, err := r.publishedFact(ctx, resourceID, version)
		if err != nil {
			return state, gate, err
		}
		if fact == nil || fact.PublishedAt.Before(approvedAt) {
			return state, gate, domain.ErrPublicationInconsistent
		}
		state.Head = &version
	}
	return state, gate, state.Validate()
}

func (r *ScriptPublicationRepository) ApplyPublication(ctx context.Context, cmd port.PublicationCommand) (domain.PublicationResult, error) {
	at := cmd.At.UTC().Truncate(time.Millisecond)
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.PublicationResult, error) {
		var empty domain.PublicationResult
		state, gate, err := r.loadState(tx, cmd.ResourceID)
		if err != nil {
			return empty, err
		}
		if err := state.CheckRevision(cmd.Action, cmd.ExpectedRevision); err != nil {
			return empty, err
		}
		var target *domain.PublishedScript
		var fact *scriptPublishedFactDocument
		if cmd.Action == domain.PublishScript {
			version, approvedAt, err := r.approvedVersion(tx, cmd.ResourceID, cmd.VersionID)
			if err != nil {
				return empty, err
			}
			if at.Before(approvedAt) {
				return empty, domain.ErrPublicationInconsistent
			}
			target = &version
			fact, err = r.publishedFact(tx, cmd.ResourceID, version)
			if err != nil {
				return empty, err
			}
			if fact != nil && fact.PublishedAt.Before(approvedAt) {
				return empty, domain.ErrPublicationInconsistent
			}
			if fact == nil {
				// An existing publication event without its permanent fact is damage,
				// rather than a new first publication that may recreate the fact.
				err := r.db.Collection(scriptPublicationEventsCollection).FindOne(tx,
					bson.M{"resourceId": cmd.ResourceID, "action": domain.PublishScript, "version.versionId": cmd.VersionID}).Err()
				if err == nil {
					return empty, domain.ErrPublicationInconsistent
				}
				if !errors.Is(err, dm.ErrNoDocuments) {
					return empty, err
				}
			}
		}
		next, changed, err := state.Transition(cmd.Action, target, cmd.ExpectedRevision, cmd.Actor, at)
		if err != nil || !changed {
			return domain.PublicationResult{State: next}, err
		}
		if !canonicalScriptID(string(cmd.EventID)) || gate.CoordinationRevision == math.MaxInt64 {
			return empty, domain.ErrPublicationInconsistent
		}
		reason := ""
		namespace, revision := "PUBLICATION", next.PublicationRevision
		update := bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}}
		if cmd.Action == domain.SuspendScript || cmd.Action == domain.RestoreScript {
			reason, err = domain.NormalizeDistributionReason(cmd.Reason)
			if err != nil {
				return empty, err
			}
			namespace, revision = "AVAILABILITY", next.AvailabilityRevision
			update["$set"] = bson.M{"state": next.Availability, "revision": next.AvailabilityRevision,
				"updatedBy": next.AvailabilityUpdatedBy, "updatedAt": next.AvailabilityUpdatedAt}
		}
		// Every actual head/gate change writes this same document. A competing
		// Suspend therefore makes a Publish retry its availability observation.
		locked, err := r.db.Collection(scriptAvailabilityCollection).UpdateOne(tx,
			bson.M{"resourceId": cmd.ResourceID, "coordinationRevision": gate.CoordinationRevision}, update)
		if err != nil {
			return empty, err
		}
		if locked.ModifiedCount != 1 {
			return empty, domain.ErrPublicationInconsistent
		}
		if namespace == "PUBLICATION" {
			var head *string
			if next.Head != nil {
				id := string(next.Head.VersionID)
				head = &id
			}
			written, err := r.db.Collection(scriptPublicationsCollection).UpdateOne(tx,
				bson.M{"resourceId": cmd.ResourceID, "revision": state.PublicationRevision},
				bson.M{"$set": bson.M{"head": head, "revision": next.PublicationRevision, "updatedBy": cmd.Actor, "updatedAt": at}})
			if err != nil {
				return empty, err
			}
			if written.ModifiedCount != 1 {
				return empty, domain.ErrPublicationRevisionConflict
			}
		}
		var descriptor *scriptPublishedVersionDocument
		if target != nil {
			value := scriptPublishedVersion(*target)
			descriptor = &value
			if fact == nil {
				_, err := r.db.Collection(scriptPublishedVersionsCollection).InsertOne(tx, scriptPublishedFactDocument{
					ResourceID: string(cmd.ResourceID), Version: value, FirstEventID: string(cmd.EventID), PublishedBy: cmd.Actor.String(), PublishedAt: at,
				})
				if err != nil {
					return empty, err
				}
			}
		}
		event := scriptPublicationEventDocument{EventID: string(cmd.EventID), ResourceID: string(cmd.ResourceID), Namespace: namespace,
			Action: cmd.Action, Revision: revision, Version: descriptor, Actor: cmd.Actor.String(), At: at, Reason: reason}
		if _, err := r.db.Collection(scriptPublicationEventsCollection).InsertOne(tx, event); err != nil {
			return empty, err
		}
		return domain.PublicationResult{State: next, Event: &domain.PublicationEvent{ID: cmd.EventID, ResourceID: cmd.ResourceID,
			Action: cmd.Action, Revision: revision, Version: target, Actor: cmd.Actor, At: at, Reason: reason}}, nil
	})
}
