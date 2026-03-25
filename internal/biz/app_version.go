package biz

import (
	"context"
	"time"
)

type AppVersionRepo interface {
	GetApplicationVersionInfo(ctx context.Context, clientId string, version int32) (*ApplicationVersionInfo, error)
	GetApplicationVersionInfoWithUserCheck(ctx context.Context, clientId string, version int32, uid string) (bool, *ApplicationVersionInfo, error)
	CreateAppVersion(ctx context.Context, versionInfo ApplicationVersionInfo, uid string) (*ApplicationVersionInfo, error)
	DeleteAppVersion(ctx context.Context, clientId string, version int32, uid string) error
}
type AppVersionUsecase struct {
	Repo AppVersionRepo
}

func NewAppVersionUsecase(repo AppVersionRepo) *AppVersionUsecase {
	return &AppVersionUsecase{
		Repo: repo,
	}
}

// ApplicationVersionInfo 这个名字不太好
// 它想表达的意思是 某Application特定版本的信息
type ApplicationVersionInfo struct {
	ClientId        string     `bson:"client_id"`
	InternalVersion int32      `bson:"internal_version"`
	BasicScope      []string   `bson:"basic_scope"`
	OptionalScope   []string   `bson:"optional_scope"`
	Version         string     `bson:"version"`
	DisplayName     string     `bson:"display_name"`
	Description     string     `bson:"description"`
	Url             string     `bson:"url"`    // 首次访问Url
	Icon            string     `bson:"icon"`   // 图标Url
	Status          string     `bson:"status"` // DEACTIVATE STABLE GREY TEST
	Tester          *[]string  `bson:"tester"` // 测试用户列表，仅beta版本有意义
	CreatedAt       time.Time  `bson:"created_at"`
	DeletedAt       *time.Time `bson:"deleted_at"`
}

const (
	ApplicationVersionInfoStableStatus     = "STABLE"
	ApplicationVersionInfoGreyStatus       = "GREY"
	ApplicationVersionInfoTestStatus       = "TEST"
	ApplicationVersionInfoDeactivateStatus = "DEACTIVATE"
)
