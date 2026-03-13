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
	"github.com/samber/lo"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type appVersionRepo struct {
	data                         *Data
	applicationCollection        *mongo.Collection
	applicationVersionCollection *mongo.Collection
	configCenterUtil             *util.ConfigCenterUtil
	greyCalc                     *util.GreyCalc
	log                          *log.Helper
}

func NewAppVersionRepo(data *Data, c *conf.Data, greyCalc *util.GreyCalc, logger log.Logger) biz.AppVersionRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &appVersionRepo{
		data:                         data,
		applicationCollection:        data.mongo.Database(dbName).Collection("application"),
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		greyCalc:                     greyCalc,
		log:                          log.NewHelper(logger),
	}
}
func (r *appVersionRepo) GetApplicationVersionInfo(ctx context.Context, clientId string, version int32) (*biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	result, err := r.getApplicationVersionInfo(ctx, clientId, version)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetApplicationVersionInfo no document found for clientId: %s, version: %d", clientId, version)
			return nil, errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client or version not found")
		}
		l.Errorf("GetApplicationVersionInfo error: %v", err)
		return nil, err
	}
	return &result, nil
}

func (r *appVersionRepo) GetApplicationVersionInfoWithUserCheck(parentCtx context.Context, clientId string, version int32, uid string) (bool, *biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	filter := bson.M{"client_id": clientId}
	var applicationResult biz.Application
	err := r.applicationCollection.FindOne(ctx, filter).Decode(&applicationResult)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetApplicationInfo no document found for clientId: %s", clientId)
			return false, nil, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("GetApplicationInfo error: %v", err)
		return false, nil, err
	}

	versionNotAllowed := false
	if applicationResult.StableVersion != version && applicationResult.GreyVersion != version && applicationResult.BetaVersion != version {
		versionNotAllowed = true
	}

	result, err := r.getApplicationVersionInfo(parentCtx, clientId, version)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("GetApplicationVersionInfo no document found for clientId: %s, version: %d", clientId, version)
			return false, nil, errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client or version not found")
		}
		l.Errorf("GetApplicationVersionInfo error: %v", err)
		return false, nil, err
	}
	if versionNotAllowed {
		return false, &result, nil
	}
	if applicationResult.BetaVersion == version {
		if result.Tester != nil && lo.Contains(*result.Tester, uid) {
			return true, &result, nil
		}
		return false, &result, nil
	}
	if useGrey, err := r.greyCalc.IsUseGrey(uid, applicationResult.GreyShuffleCode, applicationResult.GreyPercentage); err == nil {
		if useGrey {
			if applicationResult.GreyVersion == version {
				return true, &result, nil
			}
			return false, &result, nil
		}
		if applicationResult.StableVersion == version {
			return true, &result, nil
		}
		return false, &result, nil
	} else {
		return false, nil, err
	}
}

func (r *appVersionRepo) CreateAppVersion(parentCtx context.Context, versionInfo biz.ApplicationVersionInfo) (*biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	nextVersion, err := r.existsClientIDAndGetNextVersion(ctx, versionInfo.ClientId)
	cancel()
	if err != nil {
		return nil, err
	}
	versionInfo.InternalVersion = nextVersion

	ctx, cancel = context.WithTimeout(parentCtx, 5*time.Second)
	update := bson.M{"$inc": bson.M{"next_version": 1}}
	_, err = r.applicationCollection.UpdateOne(ctx, bson.M{"client_id": versionInfo.ClientId}, update)
	cancel()
	if err != nil {
		l.Errorf("CreateAppVersion error updating next version: %v", err)
		return nil, err
	}

	ctx, cancel = context.WithTimeout(parentCtx, 5*time.Second)
	allowedScope := r.configCenterUtil.GetAllowedScope()
	cancel()
	for _, scope := range versionInfo.BasicScope {
		if _, ok := allowedScope[scope]; !ok {
			l.Debugf("CreateAppVersion invalid scope: %s", scope)
			return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_SCOPE), "invalid scope: "+scope)
		}
	}
	for _, scope := range versionInfo.OptionalScope {
		if _, ok := allowedScope[scope]; !ok {
			l.Debugf("CreateAppVersion invalid scope: %s", scope)
			return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_SCOPE), "invalid scope: "+scope)
		}
	}
	if len(versionInfo.Version) > 50 {
		l.Debugf("CreateAppVersion too large: %s", versionInfo.Version)
		return nil, errors.BadRequest(string(v1.ErrorReason_VERSION_TOO_LONG), "version length must be less than 50")
	}

	if len(versionInfo.DisplayName) > 20 {
		l.Debugf("CreateAppVersion too long display name: %s", versionInfo.DisplayName)
		return nil, errors.BadRequest(string(v1.ErrorReason_NAME_TOO_LONG), "display name length must be less than 20")
	}

	if len(versionInfo.Description) > 200 {
		l.Debugf("CreateAppVersion too long description: %s", versionInfo.Description)
		return nil, errors.BadRequest(string(v1.ErrorReason_DESCRIPTION_TOO_LONG), "description length must be less than 200")
	}

	if !util.IsHttpURL(versionInfo.Url) {
		l.Debugf("CreateAppVersion invalid url: %s", versionInfo.Url)
		return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid url: "+versionInfo.Url)
	}

	if !util.IsHttpURL(versionInfo.Icon) {
		l.Debugf("CreateAppVersion invalid icon url: %s", versionInfo.Icon)
		return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid icon url: "+versionInfo.Icon)
	}

	versionInfo.Status = "DEACTIVATE"
	versionInfo.Tester = nil
	versionInfo.CreatedAt = time.Now()
	versionInfo.DeletedAt = nil

	ctx, cancel = context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	_, err = r.applicationVersionCollection.InsertOne(ctx, versionInfo)
	if err != nil {
		l.Errorf("CreateApplication error inserting application: %v", err)
		return nil, err
	}
	return &versionInfo, nil
}

func (r *appVersionRepo) existsClientIDAndGetNextVersion(ctx context.Context, clientId string) (int32, error) {
	var result struct {
		ClientId    string `bson:"client_id"`
		NextVersion int32  `bson:"next_version"`
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	opts := options.FindOne().SetProjection(bson.M{"client_id": 1, "next_version": 1})
	err := r.applicationCollection.FindOne(ctx, bson.M{"client_id": clientId}, opts).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		return 0, err
	}
	return result.NextVersion, nil
}

func (r *appVersionRepo) getApplicationVersionInfo(ctx context.Context, clientId string, internalVersion int32) (biz.ApplicationVersionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	var result biz.ApplicationVersionInfo
	err := r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
	return result, err
}
