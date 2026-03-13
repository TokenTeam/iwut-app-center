package biz

import (
	"context"
	"iwut-app-center/internal/util"
	"time"
)

type AppRepo interface {
	GetApplicationInfo(ctx context.Context, clientId string) (*Application, error)
	CreateApplication(ctx context.Context, admin string, name string) (*Application, error)
	GetAppList(ctx context.Context, uid string) ([]AppListItem, error)
	UpdateApplicationRule(ctx context.Context, clientId string, uid string, rule *util.Rule) error
	RefreshApplicationSecret(ctx context.Context, clientId string, uid string) (string, error)
	UpdateApplicationRedirectUri(ctx context.Context, clientId string, uid string, redirectUri []string) error
	UpdateApplicationVersionStatus(ctx context.Context, clientId string, internalVersion int32, uid string, status string) error
	UpdateApplicationGreyPercentage(ctx context.Context, clientId string, uid string, greyPercentage float64) error
	UpdateApplicationGreyShuffleCode(ctx context.Context, clientId string, uid string) error
}
type AppUsecase struct {
	Repo AppRepo
}

func NewAppUsecase(repo AppRepo) *AppUsecase {
	return &AppUsecase{
		Repo: repo,
	}
}

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
	//	Id string // 计算属性！ 应用ID，格式为 admin.name
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

const (
	ApplicationStatusDeveloping = "DEVELOPING"
	ApplicationStatusAuditing   = "AUDITING"
	ApplicationStatusPublished  = "PUBLISHED"
	ApplicationStatusBanned     = "BANNED"
	ApplicationStatusHidden     = "HIDDEN"
)
