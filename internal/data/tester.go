package data

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type testerRepo struct {
	data                         *Data
	applicationVersionCollection *mongo.Collection
	log                          *log.Helper
}

func NewTesterRepo(data *Data, c *conf.Data, logger log.Logger) biz.TesterRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &testerRepo{
		data:                         data,
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		log:                          log.NewHelper(logger),
	}
}

func (r *testerRepo) SaveInvite(ctx context.Context, id string, data []byte, expiration time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return r.data.redis.Set(ctx, GetRedisKey("beta_invite", id), data, expiration).Err()
}

func (r *testerRepo) GetInvite(ctx context.Context, inviteId string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := r.data.redis.Get(ctx, GetRedisKey("beta_invite", inviteId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.NotFound(string(v1.ErrorReason_INVITE_NOT_FOUND), "invite id not found")
		}
		return nil, err
	}
	return []byte(result), nil
}

func (r *testerRepo) CheckVersionNotDeleted(ctx context.Context, clientId string, betaVersion int32) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId, "internal_version": betaVersion}
	var result struct {
		DeletedAt *time.Time `bson:"deleted_at"`
	}
	err := r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.BadRequest(string(v1.ErrorReason_INVITE_NOT_FOUND), "application version not found")
		}
		return err
	}
	if result.DeletedAt != nil {
		return errors.BadRequest(string(v1.ErrorReason_INVITE_NOT_FOUND), "application version not found")
	}
	return nil
}

func (r *testerRepo) AtomicAddTester(ctx context.Context, clientId string, betaVersion int32, userId string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"client_id":        clientId,
			"internal_version": betaVersion,
		}}},
		{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$lt", Value: bson.A{
				bson.D{{Key: "$size", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$tester", bson.A{}}}}}},
				100,
			}}},
			bson.D{{Key: "$not", Value: bson.A{
				bson.D{{Key: "$in", Value: bson.A{
					userId,
					bson.D{{Key: "$ifNull", Value: bson.A{"$tester", bson.A{}}}},
				}}},
			}}},
		}}}}}}},
		{{Key: "$set", Value: bson.D{
			{Key: "tester", Value: bson.D{
				{Key: "$concatArrays", Value: bson.A{
					bson.D{{Key: "$ifNull", Value: bson.A{"$tester", bson.A{}}}},
					bson.A{userId},
				}},
			}},
		}}},
		{{Key: "$merge", Value: bson.D{
			{Key: "into", Value: "application_version"},
			{Key: "on", Value: "_id"},
			{Key: "whenMatched", Value: "merge"},
			{Key: "whenNotMatched", Value: "fail"},
		}}},
	}

	cursor, err := r.applicationVersionCollection.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer func() { _ = cursor.Close(ctx) }()

	return nil
}

func (r *testerRepo) CheckUserIsTester(ctx context.Context, clientId string, betaVersion int32, userId string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := r.applicationVersionCollection.FindOne(
		ctx,
		bson.M{
			"client_id":        clientId,
			"internal_version": betaVersion,
			"tester":           bson.M{"$in": []string{userId}},
		},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Err()

	if err == nil {
		return true, nil
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return false, err
}
