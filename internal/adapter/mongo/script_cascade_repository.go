package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	review "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const (
	scriptCascadeTasksCollection   = "script_cascade_tasks"
	scriptCascadeTargetsCollection = "script_cascade_targets"
	scriptCascadeLeaseDuration     = 30 * time.Second
	scriptCascadeBatchSize         = 32
)

type scriptCascadeTaskDocument struct {
	TaskID          string    `bson:"taskId"`
	ResourceID      string    `bson:"scriptResourceId"`
	VersionID       string    `bson:"scriptVersionId"`
	Digest          string    `bson:"digest"`
	ReviewID        string    `bson:"scriptReviewId"`
	DecisionID      string    `bson:"decisionId"`
	TriggeredBy     string    `bson:"triggeredBy"`
	DecidedAt       time.Time `bson:"decidedAt"`
	Status          string    `bson:"status"`
	Phase           string    `bson:"phase"`
	Cursor          string    `bson:"cursor"`
	Processed       int64     `bson:"processed"`
	Total           int64     `bson:"total"`
	Rejected        int64     `bson:"rejected"`
	AlreadyTerminal int64     `bson:"alreadyTerminal"`
	Closed          int64     `bson:"closed"`
	LeaseToken      string    `bson:"leaseToken"`
	LeaseUntil      time.Time `bson:"leaseUntil"`
	NextAttemptAt   time.Time `bson:"nextAttemptAt"`
	LastFailure     string    `bson:"lastFailure"`
}

type ScriptCascadeRepository struct{ db *dm.Database }

func NewScriptCascadeRepository(db *dm.Database) *ScriptCascadeRepository {
	return &ScriptCascadeRepository{db: db}
}

var _ port.CascadeRepository = (*ScriptCascadeRepository)(nil)

func (r *ScriptCascadeRepository) ClaimCascade(ctx context.Context, token string, at time.Time) (*port.CascadeLease, error) {
	if !canonicalScriptID(token) || !validScriptQueryTime(at) {
		return nil, domain.ErrInternal
	}
	at = at.UTC().Truncate(time.Millisecond)
	var task scriptCascadeTaskDocument
	err := r.db.Collection(scriptCascadeTasksCollection).FindOneAndUpdate(ctx, bson.M{
		"status": bson.M{"$in": bson.A{"PENDING", "RUNNING"}}, "nextAttemptAt": bson.M{"$lte": at}, "leaseUntil": bson.M{"$lte": at},
	}, bson.M{"$set": bson.M{"status": "RUNNING", "leaseToken": token, "leaseUntil": at.Add(scriptCascadeLeaseDuration)}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "nextAttemptAt", Value: 1}, {Key: "taskId", Value: 1}}).SetReturnDocument(options.After)).Decode(&task)
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &port.CascadeLease{TaskID: task.TaskID, Token: token}, nil
}

func validateScriptCascadeSource(ctx context.Context, db *dm.Database, task scriptCascadeTaskDocument) error {
	if !canonicalScriptID(task.TaskID) || task.Processed < 0 || task.Total < task.Processed || task.Rejected < 0 || task.AlreadyTerminal < 0 || task.Closed < 0 ||
		task.Rejected > task.Processed || task.AlreadyTerminal > task.Processed-task.Rejected ||
		task.Closed != task.Processed-task.Rejected-task.AlreadyTerminal ||
		(task.Status != "PENDING" && task.Status != "RUNNING" && task.Status != "COMPLETE") ||
		(task.Phase != "DEPENDENCIES" && task.Phase != "REVIEWS") || (task.Cursor != "" && !canonicalScriptID(task.Cursor)) ||
		!validScriptQueryTime(task.LeaseUntil) || !validScriptQueryTime(task.NextAttemptAt) ||
		(task.Status == "COMPLETE" && (task.Phase != "REVIEWS" || task.Processed != task.Total || task.LastFailure != "")) {
		return port.ErrScriptQueryInconsistent
	}
	loaded, err := loadScriptDependency(ctx, db, shared.ScriptReference{ResourceID: task.ResourceID, VersionID: task.VersionID})
	if err != nil {
		return err
	}
	d := loaded.review.Decision
	if loaded.version.State != domain.VersionRejected || loaded.version.Digest != task.Digest || string(loaded.review.ReviewID) != task.ReviewID ||
		d == nil || d.ID != task.DecisionID || d.Actor != task.TriggeredBy || !d.At.Equal(task.DecidedAt) {
		return port.ErrScriptQueryInconsistent
	}
	return nil
}

// Each step commits one target and its durable cursor together. A bounded run
// keeps transactions small while a lease permits another process to recover.
func (r *ScriptCascadeRepository) AdvanceCascade(ctx context.Context, lease port.CascadeLease, system shared.AuthID, at time.Time) error {
	if !system.IsValid() {
		return domain.ErrInternal
	}
	at = at.UTC().Truncate(time.Millisecond)
	for range scriptCascadeBatchSize {
		done, err := withScriptTransaction(ctx, r.db, func(tx context.Context) (bool, error) {
			var task scriptCascadeTaskDocument
			err := r.db.Collection(scriptCascadeTasksCollection).FindOne(tx,
				bson.M{"taskId": lease.TaskID, "leaseToken": lease.Token, "status": "RUNNING", "leaseUntil": bson.M{"$gt": at}}).Decode(&task)
			if errors.Is(err, dm.ErrNoDocuments) {
				return true, nil
			}
			if err != nil {
				return false, err
			}
			if err := validateScriptCascadeSource(tx, r.db, task); err != nil {
				return false, err
			}
			if task.Phase == "DEPENDENCIES" {
				err = r.advanceDependency(tx, &task, system, at)
			} else {
				err = r.verifyReview(tx, &task)
			}
			if err != nil {
				return false, err
			}
			task.LastFailure = ""
			task.LeaseUntil = at.Add(scriptCascadeLeaseDuration)
			result, err := r.db.Collection(scriptCascadeTasksCollection).ReplaceOne(tx,
				bson.M{"taskId": lease.TaskID, "leaseToken": lease.Token, "status": "RUNNING"}, task)
			if err != nil {
				return false, err
			}
			if result.MatchedCount != 1 {
				return false, port.ErrScriptQueryInconsistent
			}
			return task.Status == "COMPLETE", nil
		})
		if err != nil || done {
			return err
		}
	}
	_, err := r.db.Collection(scriptCascadeTasksCollection).UpdateOne(ctx,
		bson.M{"taskId": lease.TaskID, "leaseToken": lease.Token, "status": "RUNNING"},
		bson.M{"$set": bson.M{"leaseToken": "", "leaseUntil": at, "nextAttemptAt": at}})
	return err
}

func (r *ScriptCascadeRepository) advanceDependency(ctx context.Context, task *scriptCascadeTaskDocument, system shared.AuthID, at time.Time) error {
	var dependency scriptDependencyDocument
	err := r.db.Collection(scriptDependenciesCollection).FindOne(ctx,
		bson.M{"scriptVersionId": task.VersionID, "appReviewId": bson.M{"$gt": task.Cursor}},
		options.FindOne().SetSort(bson.D{{Key: "appReviewId", Value: 1}})).Decode(&dependency)
	if errors.Is(err, dm.ErrNoDocuments) {
		if task.Processed != task.Total {
			return port.ErrScriptQueryInconsistent
		}
		task.Phase, task.Cursor = "REVIEWS", ""
		return nil
	}
	if err != nil {
		return err
	}
	if dependency.Fact.Reference.ResourceID != task.ResourceID || dependency.Fact.Digest != task.Digest || dependency.Fact.ReviewID != task.ReviewID {
		return port.ErrScriptQueryInconsistent
	}
	writer := NewApplicationReviewDecisionRepository(r.db)
	appID := shared.ApplicationID(dependency.ApplicationID)
	versionID := review.ApplicationVersionID(dependency.AppVersionID)
	reviewID := review.ApplicationReviewID(dependency.AppReviewID)
	doc, err := writer.readDecisionReview(ctx, appID, versionID, reviewID)
	if err != nil {
		return err
	}
	current, err := applicationReviewFromDocument(doc)
	if err != nil {
		return err
	}
	found := false
	for _, fact := range current.ScriptDependencies() {
		if fact.VersionID == task.VersionID {
			found = frozenScriptReferenceToDocument(fact) == dependency.Fact
		}
	}
	if !found {
		return port.ErrScriptQueryInconsistent
	}

	target := scriptCascadeTargetDocument{DecisionID: task.DecisionID, ReviewID: dependency.AppReviewID, ApplicationID: dependency.ApplicationID, Result: "ALREADY_TERMINAL", ProcessedAt: at}
	if current.Status() == review.ReviewStatusPending {
		if err := lockApplicationFence(ctx, r.db, dependency.ApplicationID); err != nil {
			return err
		}
		closure, err := r.applicationClosure(ctx, dependency.ApplicationID)
		if err != nil {
			return err
		}
		if closure != nil {
			target.Result, target.Closure = "APPLICATION_CLOSED", closure
		} else {
			candidate, err := writer.LoadDecisionCandidate(ctx, appID, versionID, reviewID, system)
			if err != nil {
				return err
			}
			decision, err := review.NewScriptRejectedDecision(current.PolicyVersion(), review.ScriptRejectionCause{
				ResourceID: task.ResourceID, VersionID: task.VersionID, Digest: task.Digest, ReviewID: task.ReviewID,
				DecisionID: task.DecisionID, TriggeredBy: shared.AuthID(task.TriggeredBy)}, system, at)
			if err != nil {
				return err
			}
			if _, err := writer.decideTransaction(ctx, candidate, system, decision); err != nil {
				return err
			}
			target.Result = "REJECTED"
		}
	}
	switch target.Result {
	case "REJECTED":
		task.Rejected++
	case "APPLICATION_CLOSED":
		task.Closed++
	case "ALREADY_TERMINAL":
		task.AlreadyTerminal++
	}
	_, err = r.db.Collection(scriptCascadeTargetsCollection).InsertOne(ctx, target)
	if err != nil {
		return err
	}
	task.Cursor, task.Processed = dependency.AppReviewID, task.Processed+1
	if task.Processed > task.Total {
		return port.ErrScriptQueryInconsistent
	}
	return nil
}

// Sweep the immutable snapshots as well as the reverse index. This detects a
// missing registration instead of treating an empty dependency page as proof.
func (r *ScriptCascadeRepository) verifyReview(ctx context.Context, task *scriptCascadeTaskDocument) error {
	var doc applicationReviewDocument
	err := r.db.Collection(applicationReviewsCollectionName).FindOne(ctx,
		bson.M{"snapshot.scriptDependencies.scriptVersionId": task.VersionID, "reviewId": bson.M{"$gt": task.Cursor}},
		options.FindOne().SetSort(bson.D{{Key: "reviewId", Value: 1}})).Decode(&doc)
	if errors.Is(err, dm.ErrNoDocuments) {
		count, err := r.db.Collection(scriptDependenciesCollection).CountDocuments(ctx, bson.M{"scriptVersionId": task.VersionID})
		if err != nil {
			return err
		}
		if count != task.Total || task.Processed != task.Total {
			return port.ErrScriptQueryInconsistent
		}
		task.Status = "COMPLETE"
		return nil
	}
	if err != nil {
		return err
	}
	current, err := applicationReviewFromDocument(doc)
	if err != nil {
		return err
	}
	if current.Status() == review.ReviewStatusPending {
		var target scriptCascadeTargetDocument
		err := r.db.Collection(scriptCascadeTargetsCollection).FindOne(ctx, bson.M{"scriptDecisionId": task.DecisionID, "appReviewId": doc.ReviewID}).Decode(&target)
		if err != nil {
			return err
		}
		closure, err := r.applicationClosure(ctx, doc.ApplicationID)
		if err != nil {
			return err
		}
		if closure == nil || target.Result != "APPLICATION_CLOSED" || target.ApplicationID != doc.ApplicationID || target.Closure == nil || *target.Closure != *closure {
			return port.ErrScriptQueryInconsistent
		}
	}
	for _, fact := range current.ScriptDependencies() {
		if fact.VersionID != task.VersionID {
			continue
		}
		if fact.ResourceID != task.ResourceID || fact.Digest != task.Digest || fact.ReviewID != task.ReviewID {
			return port.ErrScriptQueryInconsistent
		}
		if _, err := loadFrozenScriptDependency(ctx, r.db, doc.ReviewID, doc.ApplicationID, doc.VersionID, fact); err != nil {
			return err
		}
	}
	task.Cursor = doc.ReviewID
	return nil
}

func (r *ScriptCascadeRepository) RecordCascadeFailure(ctx context.Context, lease port.CascadeLease, failure string, at time.Time) error {
	if failure != "SYSTEM_PRINCIPAL_UNAVAILABLE" && failure != "CASCADE_TARGET_UNAVAILABLE" {
		return domain.ErrInternal
	}
	at = at.UTC().Truncate(time.Millisecond)
	_, err := r.db.Collection(scriptCascadeTasksCollection).UpdateOne(ctx,
		bson.M{"taskId": lease.TaskID, "leaseToken": lease.Token, "status": "RUNNING"},
		bson.M{"$set": bson.M{"leaseToken": "", "leaseUntil": at, "nextAttemptAt": at.Add(5 * time.Second), "lastFailure": failure}})
	return err
}

// A closure exemption carries the existing durable closure fact. Only the caller
// processing a target holds the Application fence; verification reads a snapshot.
type scriptCascadeClosureDocument struct {
	ID string    `bson:"closureId"`
	At time.Time `bson:"closingStartedAt"`
}
type scriptCascadeTargetDocument struct {
	DecisionID    string                        `bson:"scriptDecisionId"`
	ReviewID      string                        `bson:"appReviewId"`
	ApplicationID string                        `bson:"applicationId"`
	Result        string                        `bson:"result"`
	ProcessedAt   time.Time                     `bson:"processedAt"`
	Closure       *scriptCascadeClosureDocument `bson:"closure,omitempty"`
}

func (r *ScriptCascadeRepository) applicationClosure(ctx context.Context, appID string) (*scriptCascadeClosureDocument, error) {
	var app applicationDocument
	if err := r.db.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": appID}).Decode(&app); err != nil {
		return nil, err
	}
	if app.LifecycleStatus == "ACTIVE" {
		return nil, nil
	}
	var closure applicationClosureDocument
	if err := r.db.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"applicationId": appID}).Decode(&closure); err != nil {
		return nil, err
	}
	if _, err := closureFromDocument(closure, app.LifecycleStatus, app.LifecycleRevision); err != nil {
		return nil, err
	}
	return &scriptCascadeClosureDocument{ID: closure.ClosureID, At: closure.ClosingStartedAt}, nil
}
