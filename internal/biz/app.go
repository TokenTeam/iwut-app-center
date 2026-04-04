package biz

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/util"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/samber/lo"
)

// AppRepo defines atomic data-access operations on the application collection.
type AppRepo interface {
	GetApp(ctx context.Context, clientId string) (*Application, error)
	GetPublishedApps(ctx context.Context) ([]Application, error)
	InsertApp(ctx context.Context, app *Application) error
	ExistsClientID(ctx context.Context, clientId string) (bool, error)
	ExistsAdminName(ctx context.Context, admin, name string) (bool, error)

	SetRule(ctx context.Context, clientId string, rule *util.Rule) error
	SetSecret(ctx context.Context, clientId string, secret string) error
	SetRedirectUri(ctx context.Context, clientId string, uris []string) error
	SetGreyPercentage(ctx context.Context, clientId string, pct float64) error
	SetGreyShuffleCode(ctx context.Context, clientId string, code uint32) error
	SetName(ctx context.Context, clientId string, name string) error
	SetStatus(ctx context.Context, clientId string, status string) error
	SetCollaborators(ctx context.Context, clientId string, collaborators []string) error

	ClearVersionRef(ctx context.Context, clientId string, refKey string) error
	ClearGreyInfo(ctx context.Context, clientId string) error

	// UpdateVersionRefs atomically $set the given fields and returns the document BEFORE update.
	UpdateVersionRefs(ctx context.Context, clientId string, updates map[string]any) (*Application, error)

	// AllocateNextVersion atomically increments next_version and returns the value BEFORE increment.
	AllocateNextVersion(ctx context.Context, clientId string) (int32, error)
}

type AppUsecase struct {
	repo        AppRepo
	versionRepo AppVersionRepo
	authCenter  *util.AuthCenterUtil
	ruleParser  *util.RuleParser
	greyCalc    *util.GreyCalc
	log         *log.Helper
}

func NewAppUsecase(repo AppRepo, versionRepo AppVersionRepo, authCenter *util.AuthCenterUtil, ruleParser *util.RuleParser, greyCalc *util.GreyCalc, logger log.Logger) *AppUsecase {
	return &AppUsecase{
		repo:        repo,
		versionRepo: versionRepo,
		authCenter:  authCenter,
		ruleParser:  ruleParser,
		greyCalc:    greyCalc,
		log:         log.NewHelper(logger),
	}
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type Application struct {
	ClientId        string     `bson:"client_id"`
	ClientSecret    string     `bson:"client_secret"`
	StableVersion   int32      `bson:"stable_version"`    // 稳定版本
	GreyVersion     int32      `bson:"grey_version"`      // 灰度版本
	BetaVersion     int32      `bson:"beta_version"`      // 测试版本
	GreyPercentage  float64    `bson:"grey_percentage"`   // 灰度版本用户占比，0-1
	GreyShuffleCode uint32     `bson:"grey_shuffle_code"` // 灰度版本随机分流码，整数，由grey_calc 生成
	Name            string     `bson:"name"`              // 仅允许字母、数字、下划线、中划线
	Status          string     `bson:"status"`            // DEVELOPING AUDITING PUBLISHED BANNED（由官方下架） HIDDEN（由用户下架） ...
	Admin           string     `bson:"admin"`             // 拥有人
	Collaborators   []string   `bson:"collaborators"`     // 协作者
	NextVersion     int32      `bson:"next_version"`      // 下一个版本号，初始值为1，每次发布新版本后自增1
	CreatedAt       time.Time  `bson:"created_at"`
	RedirectUri     []string   `bson:"redirect_uri"` // 跳转Url
	Rule            *util.Rule `bson:"rule"`         // 过滤规则
}

type AppListItem struct {
	ApplicationVersionInfo
	Name          string   `bson:"name"`
	Admin         string   `bson:"admin"`
	Collaborators []string `bson:"collaborators"`
}

func (a *Application) Id() string {
	return a.Admin + "." + a.ClientId
}

func (a *Application) HasDeveloperPermission(uid string) bool {
	return a.Admin == uid || lo.Contains(a.Collaborators, uid)
}

const (
	ApplicationStatusDeveloping = "DEVELOPING"
	ApplicationStatusAuditing   = "AUDITING"
	ApplicationStatusPublished  = "PUBLISHED"
	ApplicationStatusBanned     = "BANNED"
	ApplicationStatusHidden     = "HIDDEN"
)

// ---------------------------------------------------------------------------
// Permission helpers
// ---------------------------------------------------------------------------

func (uc *AppUsecase) checkDeveloperPermission(ctx context.Context, clientId, uid string) error {
	app, err := uc.repo.GetApp(ctx, clientId)
	if err != nil {
		return err
	}
	if app.HasDeveloperPermission(uid) {
		return nil
	}
	return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
}

func (uc *AppUsecase) checkIsAdmin(ctx context.Context, uid string) (bool, error) {
	result, err := uc.authCenter.GetUserClaimByUid(ctx, uid, []string{"is_admin"})
	if err != nil {
		return false, err
	}
	if val, ok := result["is_admin"]; !ok || val != true {
		return false, nil
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Business methods
// ---------------------------------------------------------------------------

func (uc *AppUsecase) GetApplicationInfo(ctx context.Context, clientId string) (*Application, error) {
	return uc.repo.GetApp(ctx, clientId)
}

func (uc *AppUsecase) CreateApplication(ctx context.Context, admin, name string) (*Application, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if !util.IsASCIIAlphaNumDashUnderscore(name) || len(name) > 50 {
		return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_APP_NAME),
			"invalid app name: must be 50 characters or less and contain only letters, numbers, dashes, or underscores")
	}

	if exist, err := uc.repo.ExistsAdminName(ctx, admin, name); err != nil || exist {
		if err != nil {
			l.Errorf("CreateApplication error checking existing admin and name: %v", err)
			return nil, err
		}
		return nil, errors.BadRequest(string(v1.ErrorReason_APP_NAME_ALREADY_EXISTS), "app name already exists for this admin")
	}

	for tryTimes := 3; tryTimes > 0; tryTimes-- {
		clientId := util.MustUUIDv7String()
		if exist, err := uc.repo.ExistsClientID(ctx, clientId); err != nil || exist {
			if err != nil {
				l.Errorf("CreateApplication error checking existing client ID: %v", err)
				return nil, err
			}
			l.Warnf("CreateApplication generated duplicate client ID: %s, retrying...", clientId)
			continue
		}

		clientSecret, err := util.GenerateString(40)
		if err != nil {
			l.Errorf("CreateApplication error generating client secret: %v", err)
			return nil, err
		}

		app := &Application{
			ClientId:        clientId,
			ClientSecret:    clientSecret,
			StableVersion:   -1,
			GreyVersion:     -1,
			BetaVersion:     -1,
			GreyPercentage:  0,
			GreyShuffleCode: 0,
			Name:            name,
			Status:          ApplicationStatusDeveloping,
			Admin:           admin,
			Collaborators:   nil,
			NextVersion:     0,
			CreatedAt:       time.Now(),
			RedirectUri:     nil,
			Rule:            nil,
		}

		if err := uc.repo.InsertApp(ctx, app); err != nil {
			l.Errorf("CreateApplication error inserting application: %v", err)
			return nil, err
		}
		return app, nil
	}
	return nil, errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR),
		"failed to generate unique client ID after multiple attempts")
}

func (uc *AppUsecase) GetAppList(ctx context.Context, uid string) ([]AppListItem, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	publishedApps, err := uc.repo.GetPublishedApps(ctx)
	if err != nil {
		l.Errorf("GetAppList error: %v", err)
		return nil, err
	}

	finalResult := make([]AppListItem, 0, len(publishedApps))
	fields := make(map[string]any)
	appRule := make(map[string]func(map[string]any) (bool, error))

	// Phase 1: beta tester check + rule parsing
	remainingApps := make([]Application, 0, len(publishedApps))
	for _, app := range publishedApps {
		if app.StableVersion == -1 && app.GreyVersion == -1 && app.BetaVersion == -1 {
			continue
		}

		if app.BetaVersion != -1 {
			result, err := uc.versionRepo.GetVersionIfUserIsTester(ctx, app.ClientId, app.BetaVersion, uid)
			if err == nil && result != nil {
				if result.DeletedAt != nil {
					// 应用被删除时 清空test version 设置为-1
					l.Warnf("GetAppList beta version deleted for clientId: %s, version: %d", app.ClientId, app.BetaVersion)
					if clearErr := uc.repo.ClearVersionRef(ctx, app.ClientId, "beta_version"); clearErr != nil {
						l.Errorf("GetAppList error clearing beta version ref for app %s: %v", app.ClientId, clearErr)
					}
					continue
				}
				finalResult = append(finalResult, AppListItem{
					ApplicationVersionInfo: *result,
					Name:                   app.Name,
					Admin:                  app.Admin,
					Collaborators:          app.Collaborators,
				})
				continue
			}
		}

		localFields, ruleFunc, err := uc.ruleParser.GetFilterFunc(app.Rule, uc.ruleParser.GetFilterFuncId(app.ClientId, app.StableVersion))
		if err != nil {
			l.Errorf("GetAppList error parsing rule for app %s: %v", app.ClientId, err)
		}
		lo.Assign(fields, localFields)
		appRule[app.ClientId] = ruleFunc
		remainingApps = append(remainingApps, app)
	}

	// Phase 2: rule evaluation
	userProfile, err := uc.authCenter.GetUserProfileByUid(ctx, uid, lo.Keys(fields))
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

	filteredApps := make([]Application, 0, len(remainingApps))
	for _, app := range remainingApps {
		ruleFunc := appRule[app.ClientId]
		legal, err := ruleFunc(userProfileMap)
		if err != nil {
			l.Errorf("GetAppList error evaluating rule for app %s: %v", app.ClientId, err)
			continue
		}
		if legal {
			filteredApps = append(filteredApps, app)
		}
	}

	// Phase 3: grey / stable version resolution
	for _, app := range filteredApps {
		useGrey := false
		if app.GreyVersion != -1 {
			useGrey, err = uc.greyCalc.IsUseGrey(uid, app.GreyShuffleCode, app.GreyPercentage)
			if err != nil {
				l.Warnf("GetAppList error calculating grey for app %s: %v", app.ClientId, err)
				_, setErr := uc.greyCalc.GetRandomGreyShuffleCode(), error(nil)
				newCode := uc.greyCalc.GetRandomGreyShuffleCode()
				if setErr = uc.repo.SetGreyShuffleCode(ctx, app.ClientId, newCode); setErr != nil {
					l.Errorf("GetAppList error setting shuffle code for app %s: %v", app.ClientId, setErr)
				}
			}
		}

		if useGrey {
			result, err := uc.versionRepo.GetVersion(ctx, app.ClientId, app.GreyVersion)
			if err != nil {
				l.Errorf("GetAppList error getting grey version for app %s: %v", app.ClientId, err)
				if clearErr := uc.repo.ClearGreyInfo(ctx, app.ClientId); clearErr != nil {
					l.Errorf("GetAppList error clearing grey info for app %s: %v", app.ClientId, clearErr)
				}
			} else if result.DeletedAt != nil {
				l.Warnf("GetAppList grey version deleted for clientId: %s, version: %d", app.ClientId, app.GreyVersion)
				if clearErr := uc.repo.ClearVersionRef(ctx, app.ClientId, "grey_version"); clearErr != nil {
					l.Errorf("GetAppList error clearing grey version ref for app %s: %v", app.ClientId, clearErr)
				}
			} else {
				finalResult = append(finalResult, AppListItem{
					ApplicationVersionInfo: *result,
					Name:                   app.Name,
					Admin:                  app.Admin,
					Collaborators:          app.Collaborators,
				})
				continue
			}
		}

		// Fallback to stable version
		if app.StableVersion == -1 {
			continue
		}
		result, err := uc.versionRepo.GetVersion(ctx, app.ClientId, app.StableVersion)
		if err != nil {
			l.Errorf("GetAppList error getting stable version for app %s: %v", app.ClientId, err)
			continue
		}
		if result.DeletedAt != nil {
			l.Warnf("GetAppList stable version deleted for clientId: %s, version: %d", app.ClientId, app.StableVersion)
			if clearErr := uc.repo.ClearVersionRef(ctx, app.ClientId, "stable_version"); clearErr != nil {
				l.Errorf("GetAppList error clearing stable version ref for app %s: %v", app.ClientId, clearErr)
			}
			continue
		}
		finalResult = append(finalResult, AppListItem{
			ApplicationVersionInfo: *result,
			Name:                   app.Name,
			Admin:                  app.Admin,
			Collaborators:          app.Collaborators,
		})
	}

	return finalResult, nil
}

func (uc *AppUsecase) UpdateApplicationRule(ctx context.Context, clientId, uid string, rule *util.Rule) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if legal, err := uc.ruleParser.IsRuleLegal(rule); err != nil || !legal {
		if err != nil {
			return errors.BadRequest(string(v1.ErrorReason_INVALID_RULE), "invalid rule: "+err.Error())
		}
		return errors.BadRequest(string(v1.ErrorReason_INVALID_RULE), "invalid rule")
	}

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationRule permission error for clientId: %s, uid: %s: %v", clientId, uid, err)
		return err
	}

	return uc.repo.SetRule(ctx, clientId, rule)
}

func (uc *AppUsecase) RefreshApplicationSecret(ctx context.Context, clientId, uid string) (string, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("RefreshApplicationSecret permission error for clientId: %s, uid: %s: %v", clientId, uid, err)
		return "", err
	}

	newSecret, err := util.GenerateString(40)
	if err != nil {
		l.Errorf("RefreshApplicationSecret error generating new secret: %v", err)
		return "", err
	}

	if err := uc.repo.SetSecret(ctx, clientId, newSecret); err != nil {
		return "", err
	}
	return newSecret, nil
}

func (uc *AppUsecase) UpdateApplicationRedirectUri(ctx context.Context, clientId, uid string, redirectUri []string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	for _, uri := range redirectUri {
		if !util.IsHttpURL(uri) {
			return errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid redirect uri: "+uri)
		}
	}

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationRedirectUri permission error for clientId: %s, uid: %s: %v", clientId, uid, err)
		return err
	}

	return uc.repo.SetRedirectUri(ctx, clientId, redirectUri)
}

func (uc *AppUsecase) UpdateApplicationVersionStatus(ctx context.Context, clientId string, internalVersion int32, uid, status string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if status != ApplicationVersionInfoStableStatus &&
		status != ApplicationVersionInfoGreyStatus &&
		status != ApplicationVersionInfoTestStatus &&
		status != ApplicationVersionInfoDeactivateStatus {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_STATUS), "invalid status: "+status)
	}

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationVersionStatus permission error: %v", err)
		return err
	}

	// Step 1: update version status, get pre-update info
	oldVersionInfo, err := uc.versionRepo.SetVersionStatus(ctx, clientId, internalVersion, status)
	if err != nil {
		l.Errorf("UpdateApplicationVersionStatus error updating version status: %v", err)
		return err
	}

	// Step 2: compute application-level field updates
	statusKeyMap := map[string]string{
		ApplicationVersionInfoStableStatus: "stable_version",
		ApplicationVersionInfoGreyStatus:   "grey_version",
		ApplicationVersionInfoTestStatus:   "beta_version",
	}

	oper := make(map[string]any)
	if status == ApplicationVersionInfoDeactivateStatus {
		if key, ok := statusKeyMap[oldVersionInfo.Status]; ok {
			oper[key] = int32(-1)
		}
	} else if key, ok := statusKeyMap[status]; ok {
		oper[key] = internalVersion
		if oldVersionInfo.Status != ApplicationVersionInfoDeactivateStatus {
			if oldKey, ok2 := statusKeyMap[oldVersionInfo.Status]; ok2 {
				if _, exists := oper[oldKey]; !exists {
					oper[oldKey] = int32(-1)
				}
			}
		}
	}

	if len(oper) == 0 {
		return nil
	}

	// Step 3: atomically update application and get pre-update state
	oldApp, err := uc.repo.UpdateVersionRefs(ctx, clientId, oper)
	if err != nil {
		l.Errorf("UpdateApplicationVersionStatus error updating app version refs: %v", err)
		return err
	}

	// Step 4: deactivate conflicting version if needed
	conflictCheck := map[string]int32{
		ApplicationVersionInfoStableStatus: oldApp.StableVersion,
		ApplicationVersionInfoGreyStatus:   oldApp.GreyVersion,
		ApplicationVersionInfoTestStatus:   oldApp.BetaVersion,
	}
	if clearVersion, ok := conflictCheck[status]; ok && clearVersion != -1 {
		if err := uc.versionRepo.DeactivateVersion(ctx, clientId, clearVersion); err != nil {
			l.Errorf("UpdateApplicationVersionStatus error deactivating conflicting version %d: %v", clearVersion, err)
			return err
		}
	}

	return nil
}

func (uc *AppUsecase) UpdateApplicationGreyPercentage(ctx context.Context, clientId, uid string, greyPercentage float64) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationGreyPercentage permission error: %v", err)
		return err
	}

	greyPercentage = max(greyPercentage, 0)
	greyPercentage = min(greyPercentage, 1)

	return uc.repo.SetGreyPercentage(ctx, clientId, greyPercentage)
}

func (uc *AppUsecase) UpdateApplicationGreyShuffleCode(ctx context.Context, clientId, uid string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationGreyShuffleCode permission error: %v", err)
		return err
	}

	code := uc.greyCalc.GetRandomGreyShuffleCode()
	return uc.repo.SetGreyShuffleCode(ctx, clientId, code)
}

func (uc *AppUsecase) UpdateApplicationName(ctx context.Context, clientId, uid, name string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	if !util.IsASCIIAlphaNumDashUnderscore(name) || len(name) > 50 {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_APP_NAME),
			"invalid app name: must be 50 characters or less and contain only letters, numbers, dashes, or underscores")
	}

	if err := uc.checkDeveloperPermission(ctx, clientId, uid); err != nil {
		l.Errorf("UpdateApplicationName permission error: %v", err)
		return err
	}

	if exist, err := uc.repo.ExistsAdminName(ctx, uid, name); err != nil || exist {
		if err != nil {
			l.Errorf("UpdateApplicationName error checking existing admin and name: %v", err)
			return err
		}
		return errors.BadRequest(string(v1.ErrorReason_APP_NAME_ALREADY_EXISTS), "app name already exists for this admin")
	}

	return uc.repo.SetName(ctx, clientId, name)
}

func (uc *AppUsecase) UpdateApplicationStatus(ctx context.Context, clientId, uid, status string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	optionalStatus := []string{
		ApplicationStatusAuditing, ApplicationStatusBanned, ApplicationStatusPublished,
		ApplicationStatusHidden, ApplicationStatusDeveloping,
	}
	if !lo.Contains(optionalStatus, status) {
		return errors.BadRequest(string(v1.ErrorReason_INVALID_STATUS), "invalid status: "+status)
	}

	app, err := uc.repo.GetApp(ctx, clientId)
	if err != nil {
		return err
	}

	if status == app.Status {
		return nil
	}

	// Ban/unban requires system admin
	if status == ApplicationStatusBanned || app.Status == ApplicationStatusBanned {
		isAdmin, err := uc.checkIsAdmin(ctx, uid)
		if err != nil {
			l.Errorf("UpdateApplicationStatus error checking admin: %v", err)
			return err
		}
		if !isAdmin {
			if status == ApplicationStatusBanned {
				return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to ban application")
			}
			return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to unban application")
		}
		return uc.repo.SetStatus(ctx, clientId, status)
	}

	if app.HasDeveloperPermission(uid) {
		return uc.repo.SetStatus(ctx, clientId, status)
	}

	isAdmin, err := uc.checkIsAdmin(ctx, uid)
	if err != nil {
		l.Errorf("UpdateApplicationStatus error checking admin: %v", err)
		return err
	}
	if !isAdmin {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application status")
	}

	return uc.repo.SetStatus(ctx, clientId, status)
}

func (uc *AppUsecase) UpdateApplicationCollaborators(ctx context.Context, clientId, uid string, collaborators []string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	app, err := uc.repo.GetApp(ctx, clientId)
	if err != nil {
		l.Errorf("UpdateApplicationCollaborators error finding application: %v", err)
		return err
	}
	if app.Admin != uid {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "no permission to update application collaborators")
	}

	return uc.repo.SetCollaborators(ctx, clientId, collaborators)
}
