package util

import (
	"iwut-app-center/internal/conf"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/samber/lo"
	"google.golang.org/grpc"
)

type ConfigCenterUtil struct {
	log      *log.Helper
	grpcConn *grpc.ClientConn
}

func NewConfigCenterUtil(c *conf.Service, logger log.Logger) *ConfigCenterUtil {
	if c != nil && c.GetConfig() != nil {
		_ = c.GetConfig().GetUri()
	}
	util := &ConfigCenterUtil{
		log:      log.NewHelper(logger),
		grpcConn: nil,
	}
	return util
}

func (u *ConfigCenterUtil) GetAllowedScope() map[string]any {
	// 目前先写死，后续改成从配置中心读取
	return map[string]any{"read__email": nil, "read__phone": nil, "read__gender": nil}
}

func (u *ConfigCenterUtil) GetPossibleReadScopeWithoutPrefix() map[string]any {
	scopes := lo.FilterKeys[string, any](u.GetAllowedScope(), func(s string, _ any) bool {
		if strings.HasPrefix(s, "read__") {
			return true
		}
		return false
	})
	return lo.FilterSliceToMap(scopes, func(key string) (string, any, bool) {
		return key[6:], nil, true
	})
}
