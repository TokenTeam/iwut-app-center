package mongo

import (
	"context"
	"errors"
	"fmt"
	filterdomain "iwut-app-center/internal/filter/domain"
	filterport "iwut-app-center/internal/filter/port"
	"iwut-app-center/internal/shared"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	applicationFiltersCollectionName         = "application_filters"
	applicationFilterRevisionsCollectionName = "application_filter_revisions"
)

type applicationFilterDocument struct {
	ApplicationID           string    `bson:"applicationId"`
	CurrentFilterRevisionID string    `bson:"currentFilterRevisionId"`
	Revision                int64     `bson:"revision"`
	NextSequence            int64     `bson:"nextSequence"`
	UpdatedBy               string    `bson:"updatedBy"`
	UpdatedAt               time.Time `bson:"updatedAt"`
}
type filterScalarDocument struct {
	Kind         string  `bson:"kind"`
	StringValue  *string `bson:"stringValue,omitempty"`
	IntegerValue *int32  `bson:"integerValue,omitempty"`
	BooleanValue *bool   `bson:"booleanValue,omitempty"`
}
type filterPredicateDocument struct {
	FieldKey string                `bson:"fieldKey"`
	Operator string                `bson:"operator"`
	Value    *filterScalarDocument `bson:"value,omitempty"`
}
type filterGroupDocument struct {
	Operator string               `bson:"operator"`
	Children []filterRuleDocument `bson:"children"`
}
type filterRuleDocument struct {
	Kind      string                   `bson:"kind"`
	Group     *filterGroupDocument     `bson:"group,omitempty"`
	Predicate *filterPredicateDocument `bson:"predicate,omitempty"`
}
type applicationFilterRevisionDocument struct {
	FilterRevisionID string              `bson:"filterRevisionId"`
	ApplicationID    string              `bson:"applicationId"`
	Sequence         int64               `bson:"sequence"`
	SchemaVersion    string              `bson:"schemaVersion"`
	Mode             string              `bson:"mode"`
	Rule             *filterRuleDocument `bson:"rule,omitempty"`
	PublishedBy      string              `bson:"publishedBy"`
	PublishedAt      time.Time           `bson:"publishedAt"`
}

type ApplicationFilterRepository struct{ database *drivermongo.Database }

var _ filterport.Repository = (*ApplicationFilterRepository)(nil)

func NewApplicationFilterRepository(database *drivermongo.Database) *ApplicationFilterRepository {
	return &ApplicationFilterRepository{database: database}
}

func (r *ApplicationFilterRepository) LoadForAdmin(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID) (*filterdomain.ApplicationFilter, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("load application filter: invalid repository input")
	}
	var application struct {
		AdminID string `bson:"adminId"`
	}
	err := r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.D{{Key: "id", Value: applicationID.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}}).Decode(&application)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, filterport.ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if application.AdminID != adminID.String() {
		return nil, filterport.ErrApplicationAdminRequired
	}
	return r.loadFilter(ctx, applicationID)
}

func (r *ApplicationFilterRepository) loadFilter(ctx context.Context, applicationID shared.ApplicationID) (*filterdomain.ApplicationFilter, error) {
	var document applicationFilterDocument
	err := r.database.Collection(applicationFiltersCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return filterdomain.NewDefaultApplicationFilter(applicationID)
	}
	if err != nil {
		return nil, err
	}
	if document.ApplicationID != applicationID.String() {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	var revisionDocument applicationFilterRevisionDocument
	err = r.database.Collection(applicationFilterRevisionsCollectionName).FindOne(ctx, bson.D{{Key: "filterRevisionId", Value: document.CurrentFilterRevisionID}}).Decode(&revisionDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	if err != nil {
		return nil, err
	}
	revision, err := filterRevisionFromDocument(revisionDocument)
	if err != nil {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	filter, err := filterdomain.RestoreApplicationFilter(applicationID, document.Revision, document.NextSequence, revision)
	if err != nil {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	return filter, nil
}

func (r *ApplicationFilterRepository) Commit(ctx context.Context, adminID shared.AuthID, expectedRevision int64, candidate *filterdomain.ApplicationFilter, revision *filterdomain.ApplicationFilterRevision) (*filterdomain.ApplicationFilter, error) {
	if r == nil || r.database == nil || !adminID.IsValid() || expectedRevision < 0 || candidate == nil || revision == nil || candidate.CurrentRevision() != revision || candidate.Revision() != expectedRevision+1 || revision.PublishedBy() != adminID {
		return nil, fmt.Errorf("commit application filter: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.commitTransaction(tx, adminID, expectedRevision, candidate, revision)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		if drivermongo.IsDuplicateKeyError(err) {
			return nil, filterport.ErrApplicationFilterRevisionConflict
		}
		return nil, err
	}
	committed, ok := result.(*filterdomain.ApplicationFilter)
	if !ok || committed == nil {
		return nil, fmt.Errorf("commit application filter: invalid transaction result")
	}
	return committed, nil
}

func (r *ApplicationFilterRepository) commitTransaction(ctx context.Context, adminID shared.AuthID, expectedRevision int64, candidate *filterdomain.ApplicationFilter, revision *filterdomain.ApplicationFilterRevision) (*filterdomain.ApplicationFilter, error) {
	apps := r.database.Collection(applicationsCollectionName)
	raw, err := apps.FindOneAndUpdate(ctx, bson.D{{Key: "id", Value: candidate.ApplicationID().String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, filterport.ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if raw.Lookup("adminId").Type != bson.TypeString {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	if raw.Lookup("adminId").StringValue() != adminID.String() {
		return nil, filterport.ErrApplicationAdminRequired
	}
	current, err := r.loadFilter(ctx, candidate.ApplicationID())
	if err != nil {
		return nil, err
	}
	if current.Revision() != expectedRevision {
		return nil, filterport.ErrApplicationFilterRevisionConflict
	}
	if candidate.Revision() != current.Revision()+1 || candidate.NextSequence() != current.NextSequence()+1 || revision.Sequence() != current.NextSequence() || revision.ApplicationID() != candidate.ApplicationID() {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	revisionDocument, err := filterRevisionToDocument(revision)
	if err != nil {
		return nil, filterport.ErrApplicationFilterStateInconsistent
	}
	if _, err = r.database.Collection(applicationFilterRevisionsCollectionName).InsertOne(ctx, revisionDocument); err != nil {
		return nil, err
	}
	filterDocument := applicationFilterDocument{ApplicationID: candidate.ApplicationID().String(), CurrentFilterRevisionID: revision.ID().String(), Revision: candidate.Revision(), NextSequence: candidate.NextSequence(), UpdatedBy: revision.PublishedBy().String(), UpdatedAt: revision.PublishedAt()}
	filters := r.database.Collection(applicationFiltersCollectionName)
	if expectedRevision == 0 {
		_, err = filters.InsertOne(ctx, filterDocument)
	} else {
		var update *drivermongo.UpdateResult
		update, err = filters.UpdateOne(ctx, bson.D{{Key: "applicationId", Value: candidate.ApplicationID().String()}, {Key: "revision", Value: expectedRevision}}, bson.D{{Key: "$set", Value: filterDocument}})
		if err == nil && update.MatchedCount != 1 {
			return nil, filterport.ErrApplicationFilterRevisionConflict
		}
	}
	if err != nil {
		return nil, err
	}
	return candidate, nil
}

func filterRevisionToDocument(revision *filterdomain.ApplicationFilterRevision) (applicationFilterRevisionDocument, error) {
	document := applicationFilterRevisionDocument{FilterRevisionID: revision.ID().String(), ApplicationID: revision.ApplicationID().String(), Sequence: revision.Sequence(), SchemaVersion: filterdomain.SchemaVersion, Mode: string(revision.Mode()), PublishedBy: revision.PublishedBy().String(), PublishedAt: revision.PublishedAt()}
	if rule := revision.Rule(); rule != nil {
		value, err := filterRuleToDocument(*rule)
		if err != nil {
			return applicationFilterRevisionDocument{}, err
		}
		document.Rule = &value
	}
	return document, nil
}

func filterRevisionFromDocument(document applicationFilterRevisionDocument) (*filterdomain.ApplicationFilterRevision, error) {
	applicationID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok || document.SchemaVersion != filterdomain.SchemaVersion {
		return nil, filterdomain.ErrApplicationFilterStateInconsistent
	}
	by := shared.AuthID(document.PublishedBy)
	switch filterdomain.RevisionMode(document.Mode) {
	case filterdomain.RevisionModeAllowAll:
		if document.Rule != nil {
			return nil, filterdomain.ErrApplicationFilterStateInconsistent
		}
		return filterdomain.NewAllowAllRevision(filterdomain.FilterRevisionID(document.FilterRevisionID), applicationID, document.Sequence, by, document.PublishedAt.UTC())
	case filterdomain.RevisionModeRule:
		if document.Rule == nil {
			return nil, filterdomain.ErrApplicationFilterStateInconsistent
		}
		rule, err := filterRuleFromDocument(*document.Rule)
		if err != nil {
			return nil, err
		}
		return filterdomain.NewRuleRevision(filterdomain.FilterRevisionID(document.FilterRevisionID), applicationID, document.Sequence, rule, by, document.PublishedAt.UTC())
	default:
		return nil, filterdomain.ErrApplicationFilterStateInconsistent
	}
}

func filterRuleToDocument(rule filterdomain.Rule) (filterRuleDocument, error) {
	if operator, children, ok := rule.Group(); ok {
		doc := filterRuleDocument{Kind: string(filterdomain.RuleKindGroup), Group: &filterGroupDocument{Operator: string(operator), Children: make([]filterRuleDocument, len(children))}}
		for i, child := range children {
			value, err := filterRuleToDocument(child)
			if err != nil {
				return filterRuleDocument{}, err
			}
			doc.Group.Children[i] = value
		}
		return doc, nil
	}
	field, operator, scalar, ok := rule.Predicate()
	if !ok {
		return filterRuleDocument{}, filterdomain.ErrInvalidApplicationFilter
	}
	doc := filterRuleDocument{Kind: string(filterdomain.RuleKindPredicate), Predicate: &filterPredicateDocument{FieldKey: field, Operator: string(operator)}}
	if scalar != nil {
		value := filterScalarDocument{Kind: string(scalar.Kind())}
		switch scalar.Kind() {
		case filterdomain.ScalarKindString, filterdomain.ScalarKindDate:
			v := scalar.StringValue()
			value.StringValue = &v
		case filterdomain.ScalarKindInteger:
			v := scalar.IntegerValue()
			value.IntegerValue = &v
		case filterdomain.ScalarKindBoolean:
			v := scalar.BooleanValue()
			value.BooleanValue = &v
		default:
			return filterRuleDocument{}, filterdomain.ErrInvalidApplicationFilter
		}
		doc.Predicate.Value = &value
	}
	return doc, nil
}

func filterRuleFromDocument(document filterRuleDocument) (filterdomain.Rule, error) {
	switch filterdomain.RuleKind(document.Kind) {
	case filterdomain.RuleKindGroup:
		if document.Group == nil || document.Predicate != nil {
			return filterdomain.Rule{}, filterdomain.ErrApplicationFilterStateInconsistent
		}
		children := make([]filterdomain.Rule, len(document.Group.Children))
		for i, child := range document.Group.Children {
			value, err := filterRuleFromDocument(child)
			if err != nil {
				return filterdomain.Rule{}, err
			}
			children[i] = value
		}
		return filterdomain.NewGroup(filterdomain.GroupOperator(document.Group.Operator), children)
	case filterdomain.RuleKindPredicate:
		if document.Predicate == nil || document.Group != nil {
			return filterdomain.Rule{}, filterdomain.ErrApplicationFilterStateInconsistent
		}
		var scalar *filterdomain.Scalar
		if document.Predicate.Value != nil {
			value, err := scalarFromDocument(*document.Predicate.Value)
			if err != nil {
				return filterdomain.Rule{}, err
			}
			scalar = &value
		}
		return filterdomain.NewPredicate(document.Predicate.FieldKey, filterdomain.PredicateOperator(document.Predicate.Operator), scalar)
	default:
		return filterdomain.Rule{}, filterdomain.ErrApplicationFilterStateInconsistent
	}
}
func scalarFromDocument(document filterScalarDocument) (filterdomain.Scalar, error) {
	switch filterdomain.ScalarKind(document.Kind) {
	case filterdomain.ScalarKindString:
		if document.StringValue == nil || document.IntegerValue != nil || document.BooleanValue != nil {
			break
		}
		return filterdomain.NewStringScalar(*document.StringValue)
	case filterdomain.ScalarKindDate:
		if document.StringValue == nil || document.IntegerValue != nil || document.BooleanValue != nil {
			break
		}
		return filterdomain.NewDateScalar(*document.StringValue)
	case filterdomain.ScalarKindInteger:
		if document.StringValue == nil && document.IntegerValue != nil && document.BooleanValue == nil {
			return filterdomain.NewIntegerScalar(*document.IntegerValue), nil
		}
	case filterdomain.ScalarKindBoolean:
		if document.StringValue == nil && document.IntegerValue == nil && document.BooleanValue != nil {
			return filterdomain.NewBooleanScalar(*document.BooleanValue), nil
		}
	}
	return filterdomain.Scalar{}, filterdomain.ErrApplicationFilterStateInconsistent
}
