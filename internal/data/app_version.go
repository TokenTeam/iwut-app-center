package data

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type appVersionRepo struct {
	data                         *Data
	applicationVersionCollection *mongo.Collection
	log                          *log.Helper
}

func NewAppVersionRepo(data *Data, c *conf.Data, logger log.Logger) biz.AppVersionRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &appVersionRepo{
		data:                         data,
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		log:                          log.NewHelper(logger),
	}
}

func (r *appVersionRepo) GetVersion(ctx context.Context, clientId string, version int32) (*biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId, "internal_version": version}
	var result biz.ApplicationVersionInfo
	err := r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetVersion no document found for clientId: %s, version: %d", clientId, version)
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client or version not found")
		}
		l.Errorf("GetVersion error: %v", err)
		return nil, err
	}
	return &result, nil
}

func (r *appVersionRepo) GetVersionIfUserIsTester(ctx context.Context, clientId string, betaVersion int32, uid string) (*biz.ApplicationVersionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{
		"client_id":        clientId,
		"internal_version": betaVersion,
		"tester":           uid,
	}
	var result biz.ApplicationVersionInfo
	err := r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &result, nil
}

func (r *appVersionRepo) InsertVersion(ctx context.Context, info *biz.ApplicationVersionInfo) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := r.applicationVersionCollection.InsertOne(ctx, info)
	return err
}

func (r *appVersionRepo) SetVersionStatus(ctx context.Context, clientId string, internalVersion int32, status string) (*biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	update := bson.M{"$set": bson.M{"status": status}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.Before)

	var result biz.ApplicationVersionInfo
	err := r.applicationVersionCollection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("SetVersionStatus no document found for clientId: %s, version: %d", clientId, internalVersion)
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client version not found")
		}
		l.Errorf("SetVersionStatus error: %v", err)
		return nil, err
	}
	return &result, nil
}

func (r *appVersionRepo) SoftDeleteVersion(ctx context.Context, clientId string, internalVersion int32) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	update := bson.M{"$set": bson.M{"deleted_at": time.Now()}}
	err := r.applicationVersionCollection.FindOneAndUpdate(ctx, filter, update).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("SoftDeleteVersion no document found for clientId: %s, version: %d", clientId, internalVersion)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client or version not found")
		}
		l.Errorf("SoftDeleteVersion error: %v", err)
		return err
	}
	return nil
}

func (r *appVersionRepo) DeactivateVersion(ctx context.Context, clientId string, internalVersion int32) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	update := bson.M{"$set": bson.M{"status": biz.ApplicationVersionInfoDeactivateStatus}}
	_, err := r.applicationVersionCollection.UpdateOne(ctx, filter, update)
	return err
}
