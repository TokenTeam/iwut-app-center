package data

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"iwut-app-center/internal/util"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type appRepo struct {
	data                  *Data
	applicationCollection *mongo.Collection
	log                   *log.Helper
}

func NewAppRepo(data *Data, c *conf.Data, logger log.Logger) biz.AppRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &appRepo{
		data:                  data,
		applicationCollection: data.mongo.Database(dbName).Collection("application"),
		log:                   log.NewHelper(logger),
	}
}

func (r *appRepo) GetApp(ctx context.Context, clientId string) (*biz.Application, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var result biz.Application
	err := r.applicationCollection.FindOne(ctx, bson.M{"client_id": clientId}).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetApp no document found for clientId: %s", clientId)
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("GetApp error: %v", err)
		return nil, err
	}
	return &result, nil
}

func (r *appRepo) GetPublishedApps(ctx context.Context) ([]biz.Application, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cursor, err := r.applicationCollection.Find(ctx, bson.M{"status": biz.ApplicationStatusPublished})
	if err != nil {
		l.Errorf("GetPublishedApps error: %v", err)
		return nil, err
	}
	defer func() { _ = cursor.Close(ctx) }()

	apps := make([]biz.Application, 0)
	if err = cursor.All(ctx, &apps); err != nil {
		l.Errorf("GetPublishedApps error decoding: %v", err)
		return nil, err
	}
	return apps, nil
}

func (r *appRepo) InsertApp(ctx context.Context, app *biz.Application) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := r.applicationCollection.InsertOne(ctx, app)
	return err
}

func (r *appRepo) ExistsClientID(ctx context.Context, clientId string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.exists(ctx, bson.M{"client_id": clientId})
}

func (r *appRepo) ExistsAdminName(ctx context.Context, admin, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.exists(ctx, bson.M{"admin": admin, "name": name})
}

func (r *appRepo) SetRule(ctx context.Context, clientId string, rule *util.Rule) error {
	return r.setField(ctx, clientId, "rule", rule)
}

func (r *appRepo) SetSecret(ctx context.Context, clientId string, secret string) error {
	return r.setField(ctx, clientId, "client_secret", secret)
}

func (r *appRepo) SetRedirectUri(ctx context.Context, clientId string, uris []string) error {
	return r.setField(ctx, clientId, "redirect_uri", uris)
}

func (r *appRepo) SetGreyPercentage(ctx context.Context, clientId string, pct float64) error {
	return r.setField(ctx, clientId, "grey_percentage", pct)
}

func (r *appRepo) SetGreyShuffleCode(ctx context.Context, clientId string, code uint32) error {
	return r.setField(ctx, clientId, "grey_shuffle_code", code)
}

func (r *appRepo) SetName(ctx context.Context, clientId string, name string) error {
	return r.setField(ctx, clientId, "name", name)
}

func (r *appRepo) SetStatus(ctx context.Context, clientId string, status string) error {
	return r.setField(ctx, clientId, "status", status)
}

func (r *appRepo) SetCollaborators(ctx context.Context, clientId string, collaborators []string) error {
	return r.setField(ctx, clientId, "collaborators", collaborators)
}

func (r *appRepo) ClearVersionRef(ctx context.Context, clientId string, refKey string) error {
	validKeys := map[string]bool{"grey_version": true, "beta_version": true, "stable_version": true}
	if !validKeys[refKey] {
		return errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR), "invalid key to clear application version reference: "+refKey)
	}
	return r.setField(ctx, clientId, refKey, int32(-1))
}

func (r *appRepo) ClearGreyInfo(ctx context.Context, clientId string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{"grey_version": int32(-1), "grey_percentage": float64(0)}}
	return r.findOneAndUpdate(ctx, clientId, update)
}

func (r *appRepo) UpdateVersionRefs(ctx context.Context, clientId string, updates map[string]any) (*biz.Application, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId}
	update := bson.M{"$set": bson.M(updates)}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.Before)

	var result biz.Application
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		return nil, err
	}
	return &result, nil
}

func (r *appRepo) AllocateNextVersion(ctx context.Context, clientId string) (int32, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId}
	update := bson.M{"$inc": bson.M{"next_version": 1}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.Before).SetProjection(bson.M{"next_version": 1})

	var result struct {
		NextVersion int32 `bson:"next_version"`
	}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		return 0, err
	}
	return result.NextVersion, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (r *appRepo) exists(ctx context.Context, filter bson.M) (bool, error) {
	opts := options.FindOne().SetProjection(bson.M{"_id": 1})
	err := r.applicationCollection.FindOne(ctx, filter, opts).Err()
	if err == nil {
		return true, nil
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return false, err
}

func (r *appRepo) setField(ctx context.Context, clientId string, field string, value any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return r.findOneAndUpdate(ctx, clientId, bson.M{"$set": bson.M{field: value}})
}

func (r *appRepo) findOneAndUpdate(ctx context.Context, clientId string, update bson.M) error {
	err := r.applicationCollection.FindOneAndUpdate(ctx, bson.M{"client_id": clientId}, update).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		return err
	}
	return nil
}
