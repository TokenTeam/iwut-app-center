package biz

import (
	"context"
	"encoding/json"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/conf"
	"iwut-app-center/internal/util"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/samber/lo"
)

// TesterRepo defines atomic data-access operations for the tester invite flow.
type TesterRepo interface {
	SaveInvite(ctx context.Context, id string, data []byte, expiration time.Duration) error
	GetInvite(ctx context.Context, inviteId string) ([]byte, error)
	// AtomicAddTester adds a user to the tester list if not already present and list size < 100.
	AtomicAddTester(ctx context.Context, clientId string, betaVersion int32, userId string) error
	CheckUserIsTester(ctx context.Context, clientId string, betaVersion int32, userId string) (bool, error)
	CheckVersionNotDeleted(ctx context.Context, clientId string, betaVersion int32) error
}

type InviteInfo struct {
	ClientId    string `json:"client_id"`
	BetaVersion int32  `json:"beta_version"`
	Inviter     string `json:"inviter"`
}

type TesterUsecase struct {
	repo        TesterRepo
	appRepo     AppRepo
	versionRepo AppVersionRepo
	frontendUrl string
	log         *log.Helper
}

func NewTesterUsecase(repo TesterRepo, appRepo AppRepo, versionRepo AppVersionRepo, cs *conf.Server, logger log.Logger) *TesterUsecase {
	return &TesterUsecase{
		repo:        repo,
		appRepo:     appRepo,
		versionRepo: versionRepo,
		frontendUrl: cs.GetFrontendUrl(),
		log:         log.NewHelper(logger),
	}
}

func (uc *TesterUsecase) GetTestLink(ctx context.Context, clientId string, betaVersion int32, inviter string, expiration time.Duration) (string, error) {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	app, err := uc.appRepo.GetApp(ctx, clientId)
	if err != nil {
		l.Errorf("GetTestLink failed to get application info: %v", err)
		return "", err
	}
	if app.BetaVersion != betaVersion {
		return "", errors.NotFound(string(v1.ErrorReason_BETA_VERSION_NOT_FOUND), "application version not found")
	}
	if app.Admin != inviter && !lo.Contains(app.Collaborators, inviter) {
		return "", errors.NotFound(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}
	if expiration > 30*24*time.Hour {
		return "", errors.BadRequest(string(v1.ErrorReason_INVALID_EXPIRATION), "expiration is too long, max is 30 days")
	}

	id := util.NewObjectIDHex()
	jsonBytes, err := json.Marshal(InviteInfo{
		ClientId:    clientId,
		BetaVersion: betaVersion,
		Inviter:     inviter,
	})
	if err != nil {
		return "", err
	}

	if err := uc.repo.SaveInvite(ctx, id, jsonBytes, expiration); err != nil {
		l.Errorf("GetTestLink failed to save invite: %v", err)
		return "", errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "failed to generate invite link, redis error")
	}

	url, err := util.BuildInviteUrl(uc.frontendUrl, map[string]string{"invite_id": id})
	if err != nil {
		l.Errorf("GetTestLink failed to build invite url: %v", err)
		return "", errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "failed to build invite url")
	}
	return url, nil
}

func (uc *TesterUsecase) AddTester(ctx context.Context, inviteId, userId string) error {
	l := log.NewHelper(log.WithContext(ctx, uc.log.Logger()))

	inviteBytes, err := uc.repo.GetInvite(ctx, inviteId)
	if err != nil {
		l.Infof("AddTester invite id not found: %s", inviteId)
		return errors.NotFound(string(v1.ErrorReason_INVITE_NOT_FOUND), "invite id not found")
	}

	var inviteInfo InviteInfo
	if err := json.Unmarshal(inviteBytes, &inviteInfo); err != nil {
		l.Errorf("AddTester failed to unmarshal invite: %v", err)
		return err
	}

	app, err := uc.appRepo.GetApp(ctx, inviteInfo.ClientId)
	if err != nil {
		l.Errorf("AddTester failed to get application info: %v", err)
		return err
	}
	if app.BetaVersion != inviteInfo.BetaVersion {
		return errors.NotFound(string(v1.ErrorReason_BETA_VERSION_NOT_FOUND), "application version not found")
	}
	if app.Admin != inviteInfo.Inviter && !lo.Contains(app.Collaborators, inviteInfo.Inviter) {
		return errors.NotFound(string(v1.ErrorReason_PERMISSION_DENIED), "permission denied")
	}

	if err := uc.repo.CheckVersionNotDeleted(ctx, inviteInfo.ClientId, inviteInfo.BetaVersion); err != nil {
		return err
	}

	if err := uc.repo.AtomicAddTester(ctx, inviteInfo.ClientId, inviteInfo.BetaVersion, userId); err != nil {
		return err
	}

	isTester, err := uc.repo.CheckUserIsTester(ctx, inviteInfo.ClientId, inviteInfo.BetaVersion, userId)
	if err != nil {
		return err
	}
	if !isTester {
		return errors.BadRequest(string(v1.ErrorReason_TESTER_LIMIT_EXCEEDED), "tester list is full")
	}
	return nil
}
