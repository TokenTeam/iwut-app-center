package biz

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type AppRepo interface {
	GetApplicationInfo(ctx context.Context, clientId string) (*Application, error)
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
	InternalId     bson.ObjectID `bson:"_id"`
	ClientId       string        `bson:"client_id"`
	ClientSecret   string        `bson:"client_secret"`
	StableVersion  int32         `bson:"stable_version"`  // 稳定版本
	GrayVersion    int32         `bson:"gray_version"`    // 灰度版本
	BetaVersion    int32         `bson:"beta_version"`    // 测试版本
	GrayPercentage float64       `bson:"gray_percentage"` // 灰度版本用户占比，0-1
	Name           string        `bson:"name"`            // 仅允许字母、数字、下划线、中划线
	Status         string        `bson:"status"`          // DEVELOPING AUDITING PUBLISHED BANNED（由官方下架） HIDDEN（由用户下架） ...
	Admin          string        `bson:"admin"`           // 拥有人
	Collaborators  []string      `bson:"collaborators"`   // 协作者
	//	Id string // 计算属性！ 应用ID，格式为 admin.name
}

func (a *Application) Id() string {
	return a.Admin + "." + a.Name
}
