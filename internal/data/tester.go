package data

import (
	"context"
	"encoding/json"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"iwut-app-center/internal/util"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/samber/lo"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type InviteInfo struct {
	ClientId    string `json:"client_id"`
	BetaVersion int32  `json:"beta_version"`
	Inviter     string `json:"inviter"`
}
type testerRepo struct {
	data                         *Data
	applicationCollection        *mongo.Collection
	applicationVersionCollection *mongo.Collection
	appUsecase                   *biz.AppUsecase
	frontendUrl                  string
	log                          *log.Helper
}

func NewTesterRepo(data *Data, c *conf.Data, cs *conf.Server, appUsecase *biz.AppUsecase, logger log.Logger) biz.TesterRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &testerRepo{
		data:                         data,
		applicationCollection:        data.mongo.Database(dbName).Collection("application"),
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		appUsecase:                   appUsecase,
		frontendUrl:                  cs.GetFrontendUrl(),
		log:                          log.NewHelper(logger),
	}
}

func (r *testerRepo) GetTestLink(parentCtx context.Context, clientId string, betaVersion int32, inviter string, expiration time.Duration) (string, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))
	app, err := r.appUsecase.Repo.GetApplicationInfo(parentCtx, clientId)
	if err != nil {
		l.Errorf("failed to get application info: %v", err)
		return "", err
	}
	if app.BetaVersion != betaVersion {
		l.Infof("got beta version %d, expected %d", app.BetaVersion, betaVersion)
		return "", errors.NotFound(string(v1.ErrorReason_BETA_VERSION_NOT_FOUND), "application version not found")
	}
	if app.Admin != inviter && !lo.Contains(app.Collaborators, inviter) {
		l.Infof("inviter %s is not admin or collaborator of client_id %s", inviter, clientId)
		return "", errors.NotFound(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}
	if expiration > 30*24*time.Hour {
		l.Infof("expiration %s is too long, max is 30 days", expiration)
		return "", errors.BadRequest(string(v1.ErrorReason_INVALID_EXPIRATION), "expiration is too long, max is 30 days")
	}
	id := util.NewObjectIDHex()
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()
	jsonBytes, err := json.Marshal(
		InviteInfo{
			ClientId:    clientId,
			BetaVersion: betaVersion,
			Inviter:     inviter,
		},
	)
	if err != nil {
		return "", err
	}

	err = r.data.redis.Set(ctx, GetRedisKey("beta_invite", id), jsonBytes, expiration).Err()
	if err != nil {
		l.Errorf("failed to set redis key: %v", err)
		return "", errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "failed to generate invite link, redis error")
	}
	url, err := util.BuildInviteUrl(r.frontendUrl, map[string]string{"invite_id": id})
	if err != nil {
		l.Errorf("failed to build invite url: %v", err)
		return "", errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "failed to build invite url")
	}
	return url, nil
}

func (r *testerRepo) AddTester(parentCtx context.Context, inviteId, userId string) error {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	inviteByte, err := r.data.redis.Get(ctx, GetRedisKey("beta_invite", inviteId)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			l.Infof("invite id not found: %s", inviteId)
			return errors.NotFound(string(v1.ErrorReason_INVITE_NOT_FOUND), "invite id not found")
		}
	}
	var inviteInfo InviteInfo
	err = json.Unmarshal([]byte(inviteByte), &inviteInfo)

	app, err := r.appUsecase.Repo.GetApplicationInfo(parentCtx, inviteInfo.ClientId)
	if err != nil {
		l.Errorf("failed to get application info: %v", err)
		return err
	}
	if app.BetaVersion != inviteInfo.BetaVersion {
		l.Infof("got beta version %d, expected %d", app.BetaVersion, inviteInfo.BetaVersion)
		return errors.NotFound(string(v1.ErrorReason_BETA_VERSION_NOT_FOUND), "application version not found")
	}
	if app.Admin != inviteInfo.Inviter && !lo.Contains(app.Collaborators, inviteInfo.Inviter) {
		l.Infof("inviter %s is not admin or collaborator of client_id %s", inviteInfo.Inviter, inviteInfo.ClientId)
		return errors.NotFound(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}
	// 已经确保链接合法 application 信息对应
	// 下一步：检查 application version是否存在， tester list是否存在， tester list是否已包含userid ，tester list 是否已满（>=100）。如果都合法且未满，原子追加 tester。

	filter := bson.M{"client_id": inviteInfo.ClientId, "internal_version": inviteInfo.BetaVersion}
	var result struct {
		DeletedAt *time.Time `bson:"deleted_at"`
	}
	err = r.applicationVersionCollection.FindOne(ctx, filter).Decode(result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.BadRequest(string(v1.ErrorReason_INVITE_NOT_FOUND), "application version not found")
		}
		return err
	}
	if result.DeletedAt != nil {
		return errors.BadRequest(string(v1.ErrorReason_INVITE_NOT_FOUND), "application version not found")
	}
	pipeline := mongo.Pipeline{
		{
			{
				Key: "$match", Value: bson.M{
					"client_id":        inviteInfo.ClientId,
					"internal_version": inviteInfo.BetaVersion,
				},
			},
		},
		{
			{
				Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
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
				}}}}},
			},
		},
		{
			{
				Key: "$set", Value: bson.D{
					{Key: "tester", Value: bson.D{
						{Key: "$concatArrays", Value: bson.A{
							bson.D{{Key: "$ifNull", Value: bson.A{"$tester", bson.A{}}}},
							bson.A{userId},
						}},
					}},
				},
			},
		},
		{
			{
				Key: "$merge", Value: bson.D{
					{Key: "into", Value: "application_version"},
					{Key: "on", Value: "_id"},
					{Key: "whenMatched", Value: "merge"},
					{Key: "whenNotMatched", Value: "fail"},
				},
			},
		},
	}
	cursor, err := r.applicationVersionCollection.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}

	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)

	checkErr := r.applicationVersionCollection.FindOne(
		ctx,
		bson.M{
			"client_id":        inviteInfo.ClientId,
			"internal_version": inviteInfo.BetaVersion,
			"tester":           bson.M{"$in": []string{userId}},
		},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Err()

	if checkErr == nil {
		return nil
	}
	if errors.Is(checkErr, mongo.ErrNoDocuments) {
		return errors.BadRequest(string(v1.ErrorReason_TESTER_LIMIT_EXCEEDED), "tester list is full")
	}
	return checkErr
}

//	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))
//	if exist, err := r.existsClientIDAndHaveSpecialTestVersion(parentCtx, clientId, betaVersion); err != nil || !exist {
//		if err != nil {
//			l.Errorf("failed to check client id existence: %v", err)
//			return err
//		}
//		l.Errorf("client id does not exist: %s", clientId)
//		return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client id does not exist")
//	}
//
//	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
//	defer cancel()
//
//	versionFilter := bson.M{"client_id": clientId, "internal_version": betaVersion}
//
//	/*
//		opts := options.FindOne().SetProjection(bson.M{"_id": 1})
//		err := r.applicationCollection.FindOne(ctx, filter, opts).Err()
//		if err == nil {
//			return true, nil
//		}
//		if errors.Is(err, mongo.ErrNoDocuments) {
//			return false, nil
//		}
//		return false, err
//	*/
//
//	opts := options.FindOne().SetProjection(bson.M{"_id": 1})
//	err := r.applicationVersionCollection.FindOne(
//		ctx,
//		bson.M{
//			"client_id":        clientId,
//			"internal_version": betaVersion,
//		},
//		opts,
//	).Err()
//	if err != nil {
//		if errors.Is(err, mongo.ErrNoDocuments) {
//			return errors.NotFound(string(v1.ErrorReason_INTERNAL_VERSION_NOT_FOUND), "application version not found")
//		}
//		l.Errorf("AddTester error checking application_version: %v", err)
//		return err
//	}
//
//	opts = options.FindOne().SetProjection(bson.M{"_id": 1})
//	err = r.applicationVersionCollection.FindOne(
//		ctx,
//		bson.M{
//			"client_id":        clientId,
//			"internal_version": betaVersion,
//			"tester":           bson.M{"$in": []string{userId}},
//		},
//		opts,
//	).Err()
//	if err != nil {
//		if !errors.Is(err, mongo.ErrNoDocuments) {
//			l.Errorf("AddTester error checking existing tester: %v", err)
//			return err
//		}
//	}else {
//		return nil
//	}
//
//	// 2) 原子追加：tester 不存在 userId 且 tester 数量 < 100（或 tester 不存在/为空）
//	// 用 $expr + $size 保证并发下不会超 100。
//	filterCanAppend := bson.M{
//		"client_id":        clientId,
//		"internal_version": betaVersion,
//		"tester":           bson.M{"$nin": userId},
//		"$expr":            bson.M{"$lt": bson.A{bson.M{"$size": bson.M{"$ifNull": bson.A{"$tester", bson.A{}}}}, 100}},
//	}
//	updateAppend := bson.M{
//		"$addToSet": bson.M{"tester": userId},
//		"$set":      bson.M{"updated_at": time.Now()},
//	}
//
//	res, err = r.applicationVersionCollection.UpdateOne(ctx, filterCanAppend, updateAppend)
//	if err != nil {
//		l.Errorf("AddTester error updating tester list: %v", err)
//		return err
//	}
//	if res.ModifiedCount > 0 {
//		return nil
//	}
//
//	// 3) 到这里说明没追加成功：要么版本不存在，要么人数已满（>=100）
//	// 先判断版本是否存在
//	findErr := r.applicationVersionCollection.FindOne(
//		ctx,
//		versionFilter,
//		options.FindOne().SetProjection(bson.M{"_id": 1}),
//	).Err()
//	if findErr != nil {
//		if errors.Is(findErr, mongo.ErrNoDocuments) {
//			l.Infof("AddTester version not found: client_id=%s internal_version=%d", clientId, betaVersion)
//			return errors.NotFound(string(v1.ErrorReason_VERSION_NOT_FOUND), "application version not found")
//		}
//		l.Errorf("AddTester error checking version existence: %v", findErr)
//		return findErr
//	}
//
//	// 版本存在，但追加失败：认为 tester 已达上限
//	return errors.BadRequest(string(v1.ErrorReason_TESTER_LIMIT_EXCEEDED), "tester list is full")
//}

func (r *testerRepo) clientExists(ctx context.Context, filter bson.M) (bool, error) {
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
