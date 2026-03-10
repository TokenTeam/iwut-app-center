package data

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"iwut-app-center/internal/util"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/samber/lo"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type appRepo struct {
	data                         *Data
	applicationCollection        *mongo.Collection
	applicationVersionCollection *mongo.Collection
	configCenterUtil             *util.ConfigCenterUtil
	authCenterUtil               *util.AuthCenterUtil
	ruleParser                   *util.RuleParser
	greyCalc                     *util.GreyCalc
	log                          *log.Helper
}

func NewAppRepo(data *Data, c *conf.Data, configCenterUtil *util.ConfigCenterUtil, authCenterUtil *util.AuthCenterUtil, greyCalc *util.GreyCalc, ruleParser *util.RuleParser, logger log.Logger) biz.AppRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &appRepo{
		data:                         data,
		applicationCollection:        data.mongo.Database(dbName).Collection("application"),
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		configCenterUtil:             configCenterUtil,
		authCenterUtil:               authCenterUtil,
		ruleParser:                   ruleParser,
		greyCalc:                     greyCalc,
		log:                          log.NewHelper(logger),
	}
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

func (r *appRepo) GetApplicationVersionInfo(ctx context.Context, clientId string, version int32) (*biz.ApplicationVersionInfo, error) {
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

func (r *appRepo) GetApplicationVersionInfoWithUserCheck(ctx context.Context, clientId string, version int32, uid string) (bool, *biz.ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	applicationResult, err := r.GetApplicationInfo(ctx, clientId)
	if err != nil {
		return false, nil, err
	}
	if applicationResult == nil {
		l.Errorf("GetApplicationVersionInfo no application found for clientId: %s", clientId)
		return false, nil, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
	}
	versionNotAllowed := false
	if applicationResult.StableVersion != version && applicationResult.GreyVersion != version && applicationResult.BetaVersion != version {
		versionNotAllowed = true
	}

	result, err := r.getApplicationVersionInfo(ctx, clientId, version)
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

func (r *appRepo) CreateApplication(parentCtx context.Context, admin string, name string) (*biz.Application, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	if exist, err := r.existsAdminName(parentCtx, admin, name); err != nil || exist {
		if err != nil {
			l.Errorf("CreateApplication error checking existing admin and name: %v", err)
			return nil, err
		}
		return nil, errors.BadRequest(string(v1.ErrorReason_APP_NAME_ALREADY_EXISTS), "app name already exists for this admin")
	}

	for tryTimes := 3; tryTimes > 0; tryTimes-- {
		clientId := util.MustUUIDv7String()
		if exist, err := r.existsClientID(parentCtx, clientId); err != nil || exist {
			if err != nil {
				l.Errorf("CreateApplication error checking existing client ID: %v", err)
				return nil, err
			}
			l.Warnf("CreateApplication generated duplicate client ID: %s, retrying...", clientId)
			continue
		}
		app := &biz.Application{
			ClientId:        clientId,
			ClientSecret:    "",
			StableVersion:   -1,
			GreyVersion:     -1,
			BetaVersion:     -1,
			GreyPercentage:  0,
			GreyShuffleCode: 0,
			Name:            name,
			Status:          "DEVELOPING",
			Admin:           admin,
			Collaborators:   nil,
			NextVersion:     0,
			CreatedAt:       time.Now(),
			RedirectUri:     nil,
			Rule:            nil,
		}
		clientSecret, err := util.GenerateString(40)
		if err != nil {
			l.Errorf("CreateApplication error generating client secret: %v", err)
			return nil, err
		}
		app.ClientSecret = clientSecret

		ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
		_, err = r.applicationCollection.InsertOne(ctx, app)
		cancel()
		if err != nil {
			l.Errorf("CreateApplication error inserting application: %v", err)
			return nil, err
		}
		return app, nil
	}
	return nil, errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR), "failed to generate unique client ID after multiple attempts")
}

func (r *appRepo) CreateAppVersion(parentCtx context.Context, versionInfo biz.ApplicationVersionInfo) (*biz.ApplicationVersionInfo, error) {
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

func (r *appRepo) GetAppList(parentCtx context.Context, uid string) ([]biz.AppListItem, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	filter := bson.M{"status": biz.ApplicationStatusPublished}

	cursor, err := r.applicationCollection.Find(ctx, filter)
	if err != nil {
		cancel()
		l.Errorf("GetAppList error: %v", err)
		return nil, err
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)
	publishedApps := make([]biz.Application, 0)
	if err = cursor.All(ctx, &publishedApps); err != nil {
		cancel()
		l.Errorf("GetAppList error decoding published apps: %v", err)
		return nil, err
	}
	cancel()

	clearApplicationVersionReference := func(clientId string, key string) error {
		if key != "grey_version" && key != "beta_version" && key != "stable_version" {
			return errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR), "invalid key to clear application version reference: "+key)
		}
		ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
		defer cancel()
		update := bson.M{"$set": bson.M{key: -1}}
		err := r.applicationCollection.FindOneAndUpdate(ctx, bson.M{"client_id": clientId}, update).Err()
		return err
	}

	finalResult := make([]biz.AppListItem, 0, len(publishedApps))

	fields := make(map[string]any)
	appRule := make(map[string]func(map[string]string) (bool, error))
	for _, app := range publishedApps {
		if app.StableVersion == -1 && app.GreyVersion == -1 && app.BetaVersion == -1 {
			// 无version应用 跳过
			continue
		}
		if app.BetaVersion != -1 {
			var result biz.ApplicationVersionInfo
			if checkResult := func() error {
				ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
				defer cancel()
				filter := bson.M{"client_id": app.ClientId, "internal_version": app.BetaVersion, "tester": uid}
				return r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
			}(); checkResult == nil {
				if result.DeletedAt != nil {
					l.Warnf("GetAppList app in beta but deleted for clientId: %s, version: %d", app.ClientId, app.BetaVersion)
					err = clearApplicationVersionReference(app.ClientId, "beta_version")
					if err != nil {
						l.Errorf("GetAppList error clearing beta version reference for app %s: %v", app.ClientId, err)
					}
					continue
				}
				// 进测试计划了 那还说啥了
				finalResult = append(finalResult, biz.AppListItem{
					ApplicationVersionInfo: result,
					Name:                   app.Name,
					Admin:                  app.Admin,
					Collaborators:          app.Collaborators,
				})
				continue
			}
		}
		localFields, ruleFunc, err := r.ruleParser.GetFilterFunc(app.Rule, r.ruleParser.GetFilterFuncId(app.ClientId, app.StableVersion))
		if err != nil {
			l.Errorf("GetAppList error parsing rule for app %s: %v", app.ClientId, err)
		}
		lo.Assign(fields, localFields)
		appRule[app.ClientId] = ruleFunc
	}
	// 检查用户是否被过滤
	userProfile, err := r.authCenterUtil.GetUserProfileByUid(parentCtx, uid, lo.Keys(fields))
	if err != nil {
		l.Errorf("GetAppList error getting user profile for uid %s: %v", uid, err)
		return nil, err
	}
	userProfileMap := make(map[string]string)
	if userProfile != nil {
		if userProfile.Attrs != nil {
			lo.Assign(userProfileMap, userProfile.Attrs)
		}
		userProfileMap["uid"] = userProfile.UserId
		userProfileMap["email"] = userProfile.Email
		userProfileMap["createdAt"] = strconv.FormatInt(userProfile.CreatedAt.Unix(), 10)
		userProfileMap["updatedAt"] = strconv.FormatInt(userProfile.UpdatedAt.Unix(), 10)
	}
	removeLater := make(map[string]any)
	for _, app := range publishedApps {
		clientId := app.ClientId
		ruleFunc := appRule[clientId]
		legal, err := ruleFunc(userProfileMap)
		if err != nil {
			l.Errorf("GetAppList error evaluating rule for app %s: %v", clientId, err)
			continue
		}
		if !legal {
			removeLater[clientId] = nil
		}
	}
	lo.Filter(publishedApps, func(item biz.Application, _ int) bool {
		_, exist := removeLater[item.ClientId]
		return !exist
	})
	// grey 判断 与写入 finalResult
	for _, app := range publishedApps {
		useGrey := false
		if app.GreyVersion != -1 {
			useGrey, err = r.greyCalc.IsUseGrey(uid, app.GreyShuffleCode, app.GreyPercentage)
			if err != nil {
				// 大概率是数据库数据问题 小概率是算法问题
				l.Warnf("GetAppList error calculating use grey for app %s: %v", app.ClientId, err)
				// 修复数据可能会隐藏逻辑问题 对日志的监控显得更为重要
				_, err := r.setShuffleCode(ctx, app.ClientId, nil)
				if err != nil {
					l.Errorf("GetAppList error setting shuffle code for app %s: %v", app.ClientId, err)
				}
				// 不使用刚生成的shuffleCode 因为此处无法确保下次能正常读取
				// 所有人都用不了灰度好过随机用灰度
			}
		}
		if useGrey {
			result, err := r.GetApplicationVersionInfo(ctx, app.ClientId, app.GreyVersion)
			if err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					l.Warnf("GetAppList no document found for clientId: %s, version: %d", app.ClientId, app.GreyVersion)

					err = r.clearApplicationGreyInfo(ctx, app.ClientId)
					if err != nil {
						l.Errorf("GetAppList error clearing grey info for app %s: %v", app.ClientId, err)
					}
				} else {
					l.Errorf("GetAppList error getting grey version for app %s: %v", app.ClientId, err)
				}
			}
			if result != nil {
				if result.DeletedAt == nil {
					finalResult = append(finalResult, biz.AppListItem{
						ApplicationVersionInfo: *result,
						Name:                   app.Name,
						Admin:                  app.Admin,
						Collaborators:          app.Collaborators,
					})
					continue
				}
				l.Warnf("GetAppList app had referenced grey version but deleted for clientId: %s, version: %d", app.ClientId, app.GreyVersion)
				err = clearApplicationVersionReference(app.ClientId, "grey_version")
				if err != nil {
					l.Errorf("GetAppList error clearing grey version reference for app %s: %v", app.ClientId, err)
				}
			}
			l.Warnf("GetAppList nil result for clientId: %s, version: %d", app.ClientId, app.GreyVersion)
		}
		// 无需灰度 / 灰度出错，用正常版本
		result, err := r.GetApplicationVersionInfo(parentCtx, app.ClientId, app.StableVersion)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				l.Warnf("GetAppList no document found for clientId: %s, stable version: %d", app.ClientId, app.StableVersion)
			} else {
				l.Errorf("GetAppList error getting stable version for app %s: %v", app.ClientId, err)
			}
			continue
		}
		if result == nil {
			l.Warnf("GetAppList nil result for clientId: %s, stable version: %d", app.ClientId, app.StableVersion)
			continue
		}
		if result.DeletedAt != nil {
			l.Warnf("GetAppList app had referenced stable version but deleted for clientId: %s, version: %d", app.ClientId, app.StableVersion)
			err = clearApplicationVersionReference(app.ClientId, "stable_version")
			if err != nil {
				l.Errorf("GetAppList error clearing stable version reference for app %s: %v", app.ClientId, err)
			}
			continue
		}
		finalResult = append(finalResult, biz.AppListItem{
			ApplicationVersionInfo: *result,
			Name:                   app.Name,
			Admin:                  app.Admin,
			Collaborators:          app.Collaborators,
		})
	}
	return finalResult, nil
}

func (r *appRepo) UpdateApplicationRule(ctx context.Context, clientId string, uid string, rule *util.Rule) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	if legal, err := r.ruleParser.IsRuleLegal(rule); err != nil || !legal {
		if err != nil {
			return errors.BadRequest(string(v1.ErrorReason_INVALID_RULE), "invalid rule: "+err.Error())
		}
		return errors.BadRequest(string(v1.ErrorReason_INVALID_RULE), "invalid rule")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId}
	var result struct {
		Admin         string   `bson:"admin"`
		Collaborators []string `bson:"collaborators"`
	}
	err := r.applicationCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationRule no document found for clientId: %s", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationRule error finding application: %v", err)
		return err
	}
	if result.Admin != uid && !lo.Contains(result.Collaborators, uid) {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update rule")
	}
	err = r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"rule": rule}}).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationRule no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationRule error updating application: %v", err)
		return err
	}
	return nil
}

func (r *appRepo) UpdateApplicationRedirectUri(ctx context.Context, clientId string, uid string, redirectUri []string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	for _, uri := range redirectUri {
		if !util.IsHttpURL(uri) {
			l.Debugf("UpdateApplicationRedirectUri invalid redirect uri: %s", uri)
			return errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid redirect uri: "+uri)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId}
	var result struct {
		Admin         string   `bson:"admin"`
		Collaborators []string `bson:"collaborators"`
	}
	err := r.applicationCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationRedirectUri no document found for clientId: %s", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationRedirectUri error finding application: %v", err)
		return err
	}
	if result.Admin != uid && !lo.Contains(result.Collaborators, uid) {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update redirect uri")
	}
	err = r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"redirect_uri": redirectUri}}).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationRedirectUri no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationRedirectUri error updating application: %v", err)
		return err
	}
	return nil
}

func (r *appRepo) existsClientIDAndGetNextVersion(ctx context.Context, clientId string) (int32, error) {
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
func (r *appRepo) existsClientID(ctx context.Context, clientId string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.exists(ctx, bson.M{"client_id": clientId})
}
func (r *appRepo) existsAdminName(ctx context.Context, admin string, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.exists(ctx, bson.M{"admin": admin, "name": name})
}
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
func (r *appRepo) getApplicationVersionInfo(ctx context.Context, clientId string, internalVersion int32) (biz.ApplicationVersionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	var result biz.ApplicationVersionInfo
	err := r.applicationVersionCollection.FindOne(ctx, filter).Decode(&result)
	return result, err
}
func (r *appRepo) setShuffleCode(ctx context.Context, clientId string, shuffleCode *uint32) (uint32, error) {
	var code uint32
	if shuffleCode == nil {
		code = r.greyCalc.GetRandomGreyShuffleCode()
	} else {
		code = *shuffleCode
	}
	update := bson.M{"$set": bson.M{"grey_shuffle_code": code}}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := r.applicationCollection.FindOneAndUpdate(ctx, bson.M{"client_id": clientId}, update).Err()
	return code, err
}
func (r *appRepo) clearApplicationGreyInfo(ctx context.Context, clientId string) error {
	update := bson.M{"$set": bson.M{"grey_version": -1, "grey_percentage": 0}}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := r.applicationCollection.FindOneAndUpdate(ctx, bson.M{"client_id": clientId}, update).Err()
	return err
}
