package biz

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/util"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/samber/lo"
)

// AppVersionRepo defines atomic data-access operations on the application_version collection.
type AppVersionRepo interface {
	GetVersion(ctx context.Context, clientId string, version int32) (*ApplicationVersionInfo, error)
	GetVersionIfUserIsTester(ctx context.Context, clientId string, betaVersion int32, uid string) (*ApplicationVersionInfo, error)
	InsertVersion(ctx context.Context, info *ApplicationVersionInfo) error
	// SetVersionStatus updates the status and returns the document BEFORE update.
	SetVersionStatus(ctx context.Context, clientId string, internalVersion int32, status string) (*ApplicationVersionInfo, error)
	SoftDeleteVersion(ctx context.Context, clientId string, internalVersion int32) error
	DeactivateVersion(ctx context.Context, clientId string, internalVersion int32) error
}

type AppVersionUsecase struct {
	repo         AppVersionRepo
	appRepo      AppRepo
	configCenter *util.ConfigCenterUtil
	greyCalc     *util.GreyCalc
	log          *log.Helper
}

func NewAppVersionUsecase(repo AppVersionRepo, appRepo AppRepo, configCenter *util.ConfigCenterUtil, greyCalc *util.GreyCalc, logger log.Logger) *AppVersionUsecase {
	return &AppVersionUsecase{
		repo:         repo,
		appRepo:      appRepo,
		configCenter: configCenter,
		greyCalc:     greyCalc,
		log:          log.NewHelper(logger),
	}
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type ApplicationVersionInfo struct {
	ClientId        string     `bson:"client_id"`
	InternalVersion int32      `bson:"internal_version"`
	BasicScope      []string   `bson:"basic_scope"`
	OptionalScope   []string   `bson:"optional_scope"`
	Version         string     `bson:"version"`
	DisplayName     string     `bson:"display_name"`
	Description     string     `bson:"description"`
	Url             string     `bson:"url"`
	Icon            string     `bson:"icon"`
	Color           string     `bson:"color"`
	Label           string     `bson:"label"`
	Status          string     `bson:"status"`
	Tester          *[]string  `bson:"tester"`
	CreatedAt       time.Time  `bson:"created_at"`
	DeletedAt       *time.Time `bson:"deleted_at"`
}

const (
	ApplicationVersionInfoStableStatus     = "STABLE"
	ApplicationVersionInfoGreyStatus       = "GREY"
	ApplicationVersionInfoTestStatus       = "TEST"
	ApplicationVersionInfoDeactivateStatus = "DEACTIVATE"
)

// ---------------------------------------------------------------------------
// Business methods
// ---------------------------------------------------------------------------

func (uc *AppVersionUsecase) GetApplicationVersionInfo(ctx context.Context, clientId string, version int32) (*ApplicationVersionInfo, error) {
	return uc.repo.GetVersion(ctx, clientId, version)
}

func (uc *AppVersionUsecase) GetApplicationVersionInfoWithUserCheck(ctx context.Context, clientId string, version int32, uid string) (bool, *ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	app, err := uc.appRepo.GetApp(ctx, clientId)
	if err != nil {
		l.Errorf("GetApplicationVersionInfoWithUserCheck error getting app: %v", err)
		return false, nil, err
	}

	versionNotAllowed := app.StableVersion != version && app.GreyVersion != version && app.BetaVersion != version

	result, err := uc.repo.GetVersion(ctx, clientId, version)
	if err != nil {
		l.Errorf("GetApplicationVersionInfoWithUserCheck error getting version: %v", err)
		return false, nil, err
	}

	if versionNotAllowed {
		return false, result, nil
	}

	if app.BetaVersion == version {
		if result.Tester != nil && lo.Contains(*result.Tester, uid) {
			return true, result, nil
		}
		return false, result, nil
	}

	useGrey, err := uc.greyCalc.IsUseGrey(uid, app.GreyShuffleCode, app.GreyPercentage)
	if err != nil {
		return false, nil, err
	}
	if useGrey {
		return app.GreyVersion == version, result, nil
	}
	return app.StableVersion == version, result, nil
}

func (uc *AppVersionUsecase) CreateAppVersion(ctx context.Context, versionInfo ApplicationVersionInfo, uid string) (*ApplicationVersionInfo, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	// Permission check
	app, err := uc.appRepo.GetApp(ctx, versionInfo.ClientId)
	if err != nil {
		l.Errorf("CreateAppVersion error getting app: %v", err)
		return nil, err
	}
	if !app.HasDeveloperPermission(uid) {
		return nil, errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}

	// Allocate version number atomically
	nextVersion, err := uc.appRepo.AllocateNextVersion(ctx, versionInfo.ClientId)
	if err != nil {
		l.Errorf("CreateAppVersion error allocating next version: %v", err)
		return nil, err
	}
	versionInfo.InternalVersion = nextVersion

	// Validate scopes
	allowedScope := uc.configCenter.GetAllowedScope()
	for _, scope := range versionInfo.BasicScope {
		if _, ok := allowedScope[scope]; !ok {
			return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_SCOPE), "invalid scope: "+scope)
		}
	}
	for _, scope := range versionInfo.OptionalScope {
		if _, ok := allowedScope[scope]; !ok {
			return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_SCOPE), "invalid scope: "+scope)
		}
	}

	// Validate fields
	if len(versionInfo.Version) > 50 {
		return nil, errors.BadRequest(string(v1.ErrorReason_VERSION_TOO_LONG), "version length must be less than 50")
	}
	if len(versionInfo.DisplayName) > 20 {
		return nil, errors.BadRequest(string(v1.ErrorReason_NAME_TOO_LONG), "display name length must be less than 20")
	}
	if len(versionInfo.Description) > 200 {
		return nil, errors.BadRequest(string(v1.ErrorReason_DESCRIPTION_TOO_LONG), "description length must be less than 200")
	}
	if !util.IsHttpURL(versionInfo.Url) {
		return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_URI), "invalid url: "+versionInfo.Url)
	}

	// Set defaults
	versionInfo.Status = ApplicationVersionInfoDeactivateStatus
	versionInfo.Tester = nil
	versionInfo.CreatedAt = time.Now()
	versionInfo.DeletedAt = nil

	if err := uc.repo.InsertVersion(ctx, &versionInfo); err != nil {
		l.Errorf("CreateAppVersion error inserting version: %v", err)
		return nil, err
	}
	return &versionInfo, nil
}

func (uc *AppVersionUsecase) DeleteAppVersion(ctx context.Context, clientId string, version int32, uid string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	// Permission check
	app, err := uc.appRepo.GetApp(ctx, clientId)
	if err != nil {
		return err
	}
	if !app.HasDeveloperPermission(uid) {
		return errors.Forbidden(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}

	// Status check
	appVersion, err := uc.repo.GetVersion(ctx, clientId, version)
	if err != nil {
		l.Errorf("DeleteAppVersion error getting version: %v", err)
		return err
	}
	if appVersion.Status != ApplicationVersionInfoDeactivateStatus {
		return errors.BadRequest(string(v1.ErrorReason_ONLY_DEACTIVATE_VERSION_CAN_BE_DELETED),
			"only deactivated version can be deleted")
	}

	return uc.repo.SoftDeleteVersion(ctx, clientId, version)
}
