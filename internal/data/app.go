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
	authCenterUtil               *util.AuthCenterUtil
	ruleParser                   *util.RuleParser
	greyCalc                     *util.GreyCalc
	appVersionUsecase            *biz.AppVersionUsecase
	log                          *log.Helper
}

func NewAppRepo(data *Data, c *conf.Data, authCenterUtil *util.AuthCenterUtil, greyCalc *util.GreyCalc, ruleParser *util.RuleParser, appVersionUsecase *biz.AppVersionUsecase, logger log.Logger) biz.AppRepo {
	dbName := c.GetMongodb().GetDatabase()
	return &appRepo{
		data:                         data,
		applicationCollection:        data.mongo.Database(dbName).Collection("application"),
		applicationVersionCollection: data.mongo.Database(dbName).Collection("application_version"),
		authCenterUtil:               authCenterUtil,
		ruleParser:                   ruleParser,
		greyCalc:                     greyCalc,
		appVersionUsecase:            appVersionUsecase,
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

func (r *appRepo) CreateApplication(parentCtx context.Context, admin string, name string) (*biz.Application, error) {
	l := log.NewHelper(log.WithContext(parentCtx, r.log.Logger()))

	if util.IsASCIIAlphaNumDashUnderscore(name) == false || len(name) > 50 {
		return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_APP_NAME), "invalid app name: must be 50 characters or less and contain only letters, numbers, dashes, or underscores")
	}
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
	appRule := make(map[string]func(map[string]any) (bool, error))
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
	userProfileMap := make(map[string]any)
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
			result, err := r.appVersionUsecase.Repo.GetApplicationVersionInfo(ctx, app.ClientId, app.GreyVersion)
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
		result, err := r.appVersionUsecase.Repo.GetApplicationVersionInfo(parentCtx, app.ClientId, app.StableVersion)
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

	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationRule error checking developer permission for clientId: %s, uid: %s, error: %v", clientId, uid, err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application rule")
	}

	filter := bson.M{"client_id": clientId}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"rule": rule}}).Err()
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

func (r *appRepo) RefreshApplicationSecret(ctx context.Context, clientId string, uid string) (string, error) {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))
	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("RefreshApplicationSecret error checking developer permission for clientId: %s, uid: %s, error: %v", clientId, uid, err)
			return "", err
		}
		return "", errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to refresh application secret")
	}
	newSecret, err := util.GenerateString(40)
	if err != nil {
		l.Errorf("RefreshApplicationSecret error generating new secret for clientId: %s: %v", clientId, err)
		return "", err
	}
	filter := bson.M{"client_id": clientId}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"client_secret": newSecret}}).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("RefreshApplicationSecret no document found for clientId: %s during secret refresh", clientId)
			return "", errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("RefreshApplicationSecret error updating application secret for clientId: %s: %v", clientId, err)
		return "", err
	}
	return newSecret, nil
}

func (r *appRepo) UpdateApplicationRedirectUri(ctx context.Context, clientId string, uid string, redirectUri []string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	for _, uri := range redirectUri {
		if !util.IsHttpURL(uri) {
			l.Debugf("UpdateApplicationRedirectUri invalid redirect uri: %s", uri)
			return errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid redirect uri: "+uri)
		}
	}

	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationRedirectUri error checking developer permission for clientId: %s, uid: %s, error: %v", clientId, uid, err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update redirect uri")
	}
	filter := bson.M{"client_id": clientId}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"redirect_uri": redirectUri}}).Err()
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

func (r *appRepo) UpdateApplicationVersionStatus(ctx context.Context, clientId string, internalVersion int32, uid string, status string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	if status != biz.ApplicationVersionInfoStableStatus && status != biz.ApplicationVersionInfoGreyStatus && status != biz.ApplicationVersionInfoTestStatus && status != biz.ApplicationVersionInfoDeactivateStatus {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_STATUS), "invalid status: "+status)
	}

	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationVersionStatus error checking developer permission for clientId: %s, uid: %s, error: %v", clientId, uid, err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application version status")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 更新目标 ApplicationVersion Status
	var applicationVersionInfoBeforeUpdate biz.ApplicationVersionInfo
	filter := bson.M{"client_id": clientId, "internal_version": internalVersion}
	sr := r.applicationVersionCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"status": status}})
	if err := sr.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationVersionStatus no document found for clientId: %s, internalVersion: %d during update", clientId, internalVersion)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_VERSION_NOT_FOUNT), "client version not found")
		}
		l.Errorf("UpdateApplicationVersionStatus error updating application: %v", err)
		return err
	}
	if err := sr.Decode(&applicationVersionInfoBeforeUpdate); err != nil {
		l.Errorf("UpdateApplicationVersionStatus error decoding applicationVersionInfoBeforeUpdate update version info for clientId: %s, internalVersion: %d, error: %v", clientId, internalVersion, err)
		return err
	}
	// 更新 Application 中的目标Status Version, 如果目标之前是其他状态 则清除原先状态的版本引用
	var applicationInfoBeforeUpdate biz.Application
	filter = bson.M{"client_id": clientId}
	statusKeyMap := map[string]string{
		biz.ApplicationVersionInfoStableStatus: "stable_version",
		biz.ApplicationVersionInfoGreyStatus:   "grey_version",
		biz.ApplicationVersionInfoTestStatus:   "beta_version",
	}
	oper, err := func(status string) (bson.M, error) {
		if status == biz.ApplicationVersionInfoDeactivateStatus {
			if key, ok := statusKeyMap[applicationVersionInfoBeforeUpdate.Status]; ok {
				return bson.M{key: -1}, nil
			}
			if applicationVersionInfoBeforeUpdate.Status == biz.ApplicationVersionInfoDeactivateStatus {
				return bson.M{}, nil
			}
			return nil, errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR), "invalid application version status before update: "+applicationVersionInfoBeforeUpdate.Status)
		}
		var oper bson.M
		if key, ok := statusKeyMap[status]; ok {
			oper = bson.M{key: internalVersion}
			if applicationVersionInfoBeforeUpdate.Status != biz.ApplicationVersionInfoDeactivateStatus {
				if oldKey, ok := statusKeyMap[applicationVersionInfoBeforeUpdate.Status]; ok {
					if _, ok := oper[oldKey]; !ok {
						oper[oldKey] = -1
					} else {
						l.Warnf("UpdateApplicationVersionStatus unexpected old status key conflict for clientId: %s, internalVersion: %d, old status: %s", clientId, internalVersion, applicationVersionInfoBeforeUpdate.Status)
					}
				}
			}
			return oper, nil
		}
		return bson.M{}, nil
	}(status)
	if err != nil {
		l.Errorf("UpdateApplicationVersionStatus error updating application: %v", err)
		return err
	}
	sr = r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": oper})
	if err := sr.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationVersionStatus no document found for clientId: %s during application update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationVersionStatus error updating application for clientId: %s, internalVersion: %d, error: %v", clientId, internalVersion, err)
		return err
	}
	if err := sr.Decode(&applicationInfoBeforeUpdate); err != nil {
		l.Errorf("UpdateApplicationVersionStatus error decoding applicationInfoBeforeUpdate update version info for clientId: %s, internalVersion: %d, error: %v", clientId, internalVersion, err)
		return err
	}
	// 检查是否存在版本冲突
	conflictCheck := map[string]int32{
		biz.ApplicationVersionInfoStableStatus: applicationInfoBeforeUpdate.StableVersion,
		biz.ApplicationVersionInfoGreyStatus:   applicationInfoBeforeUpdate.GreyVersion,
		biz.ApplicationVersionInfoTestStatus:   applicationInfoBeforeUpdate.BetaVersion,
	}
	if clearVersion, _ := conflictCheck[status]; clearVersion != -1 {
		filter := bson.M{"client_id": clientId, "internal_version": clearVersion}
		update := bson.M{"$set": bson.M{"status": biz.ApplicationVersionInfoDeactivateStatus}}
		_, err := r.applicationVersionCollection.UpdateOne(ctx, filter, update)
		if err != nil {
			l.Errorf("UpdateApplicationVersionStatus error deactivating conflicting version for clientId: %s, internalVersion: %d, error: %v", clientId, clearVersion, err)
			return err
		}
	}
	return nil
}

func (r *appRepo) UpdateApplicationGreyPercentage(ctx context.Context, clientId string, uid string, greyPercentage float64) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationGreyPercentage error updating application: %v", err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application grey percentage")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	greyPercentage = max(greyPercentage, 0)
	greyPercentage = min(greyPercentage, 1)

	filter := bson.M{
		"client_id": clientId,
	}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"grey_percentage": greyPercentage}}).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationGreyPercentage no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationGreyPercentage error updating application: %v", err)
		return err
	}
	return nil
}

func (r *appRepo) UpdateApplicationGreyShuffleCode(ctx context.Context, clientId string, uid string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationGreyShuffleCode error updating application: %v", err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application grey shuffle code")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	shuffleCode := r.greyCalc.GetRandomGreyShuffleCode()
	filter := bson.M{
		"client_id": clientId,
	}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"grey_shuffle_code": shuffleCode}}).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationGreyShuffleCode no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationGreyShuffleCode error updating application: %v", err)
		return err
	}
	return nil
}

func (r *appRepo) UpdateApplicationName(ctx context.Context, clientId string, uid string, name string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))
	if util.IsASCIIAlphaNumDashUnderscore(name) == false || len(name) > 50 {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_APP_NAME), "invalid app name: must be 50 characters or less and contain only letters, numbers, dashes, or underscores")
	}
	if ok, err := r.checkDeveloperPermission(ctx, clientId, uid); err != nil || !ok {
		if err != nil {
			l.Errorf("UpdateApplicationName error updating application: %v", err)
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if exist, err := r.existsAdminName(ctx, uid, name); err != nil || exist {
		if err != nil {
			l.Errorf("UpdateApplicationName error checking existing admin and name: %v", err)
			return err
		}
		return errors.BadRequest(string(v1.ErrorReason_APP_NAME_ALREADY_EXISTS), "app name already exists for this admin")
	}
	filter := bson.M{"client_id": clientId}
	update := bson.M{"$set": bson.M{"name": name}}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, update).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationName no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationName error updating application: %v", err)
		return err
	}
	return nil
}

func (r *appRepo) UpdateApplicationStatus(ctx context.Context, clientId string, uid string, status string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	optionalStatus := []string{biz.ApplicationStatusAuditing, biz.ApplicationStatusBanned, biz.ApplicationStatusPublished, biz.ApplicationStatusHidden, biz.ApplicationStatusDeveloping}
	if !lo.Contains(optionalStatus, status) {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_STATUS), "invalid status: "+status)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result struct {
		Admin         string   `bson:"admin"`
		Collaborators []string `bson:"collaborators"`
		Status        string   `json:"status"`
	}
	if err := r.applicationCollection.FindOne(ctx, bson.M{"client_id": clientId}).Decode(&result); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			l.Debugf("UpdateApplicationStatus no document found for clientId: %s during update", clientId)
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationStatus error finding application for clientId: %s during update: %v", clientId, err)
		return err
	}
	// 更新和原值相同
	if status == result.Status {
		return nil
	}

	if status == biz.ApplicationStatusBanned || result.Status == biz.ApplicationStatusBanned {
		if ok, err := r.checkIsAdmin(ctx, uid); err != nil || !ok {
			if err != nil {
				l.Errorf("UpdateApplicationStatus error checking admin: %v", err)
				return err
			}
			if status == biz.ApplicationStatusBanned {
				return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to ban application")
			}
			if result.Status == biz.ApplicationStatusBanned {
				return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to unban application")
			}
		}
		return r.setApplicationStatus(ctx, clientId, status)
	}
	// 如果是开发者 / 协同者 允许调整
	if result.Admin == uid || lo.Contains(result.Collaborators, uid) {
		return r.setApplicationStatus(ctx, clientId, status)
	}
	if ok, err := r.checkIsAdmin(ctx, uid); err != nil || !ok {
		// 如果不是admin 不允许调整
		if err != nil {
			l.Errorf("UpdateApplicationStatus error checking admin: %v", err)
			return err
		}
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application status")
	}
	// 是admin 允许调整
	return r.setApplicationStatus(ctx, clientId, status)
}

func (r *appRepo) UpdateApplicationCollaborators(ctx context.Context, clientId string, uid string, collaborators []string) error {
	l := log.NewHelper(log.WithContext(ctx, r.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	filter := bson.M{"client_id": clientId}
	var result struct {
		Admin string `bson:"admin"`
	}

	err := r.applicationCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationCollaborators error finding application: %v", err)
		return err
	}
	if result.Admin != uid {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application collaborators")
	}

	update := bson.M{"$set": bson.M{"collaborators": collaborators}}
	if err := r.applicationCollection.FindOneAndUpdate(ctx, filter, update).Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		l.Errorf("UpdateApplicationCollaborators error updating application: %v", err)
		return err
	}
	return err
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
func (r *appRepo) checkDeveloperPermission(ctx context.Context, clientId string, uid string) (bool, error) {
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
			return false, errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
		return false, err
	}
	if result.Admin == uid || lo.Contains(result.Collaborators, uid) {
		return true, nil
	}
	return false, nil
}
func (r *appRepo) checkIsAdmin(ctx context.Context, uid string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := r.authCenterUtil.GetUserClaimByUid(ctx, uid, []string{"is_admin"})
	if err != nil {
		return false, err
	}
	if val, ok := result["is_admin"]; !ok || val != true {
		return false, nil
	}
	return true, nil
}
func (r *appRepo) setApplicationStatus(ctx context.Context, clientId string, status string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filter := bson.M{"client_id": clientId}
	update := bson.M{"$set": bson.M{"status": status}}
	err := r.applicationCollection.FindOneAndUpdate(ctx, filter, update).Err()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.NotFound(string(v1.ErrorReason_CLIENT_NOT_FOUND), "client not found")
		}
	}
	return err
}
