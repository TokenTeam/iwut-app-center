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

func (r *appRepo) UpdateApplicationInfo(ctx context.Context, clientId string) error {
	return nil
}
func (r *appRepo) GetApplicationInfo(ctx context.Context, clientId string) (*biz.Application, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	filter := bson.M{"client_id": clientId}
	var result biz.Application
	err := r.applicationCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetApplicationInfo no document found for clientId: %s", clientId)
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("GetApplicationInfo error: %v", err)
		return nil, err
	}
	return &result, nil
}
