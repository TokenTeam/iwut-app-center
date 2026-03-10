package util

import (
	"context"
	"encoding/json"
	"fmt"
	"iwut-app-center/api/gen/go/auth_center/v1/auth"
	"iwut-app-center/api/gen/go/auth_center/v1/user"
	"iwut-app-center/internal/conf"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	otelgrpc "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type UserProfile struct {
	UserId    string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Attrs     map[string]string
}
type AuthCenterUtil struct {
	log        *log.Helper
	grpcConn   *grpc.ClientConn
	authClient auth.AuthClient
	userClient user.UserClient
}

func NewAuthCenterUtil(c *conf.Service, logger log.Logger) (*AuthCenterUtil, func(), error) {
	l := log.NewHelper(logger)
	addr := ""
	if c != nil && c.GetAuth() != nil {
		addr = c.GetAuth().GetUri()
	}
	if addr == "" {
		return nil, nil, fmt.Errorf("auth center uri is empty in config")
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: 5 * time.Second}),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		l.Errorf("failed to create auth center client %s: %v", addr, err)
		return nil, nil, err
	}

	util := &AuthCenterUtil{
		log:        log.NewHelper(logger),
		grpcConn:   conn,
		authClient: auth.NewAuthClient(conn),
		userClient: user.NewUserClient(conn),
	}
	cleanup := func() {
		_ = conn.Close()
	}
	return util, cleanup, nil
}

func (u *AuthCenterUtil) GetUserProfileByUid(ctx context.Context, uid string, keys []string) (*UserProfile, error) {
	l := log.NewHelper(log.WithContext(ctx, u.log.Logger()))

	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	l.Debugf("GetUserInfoByUid uid: %s", uid)

	return u.getUserInfoFromAuthCenter(ctx, uid, keys)
}

type ServiceClaim struct {
	ServiceName string `json:"ServiceName"`
	FuncName    string `json:"FuncName"`
}

func (u *AuthCenterUtil) getUserInfoFromAuthCenter(ctx context.Context, uid string, keys []string) (*UserProfile, error) {
	if u.userClient == nil {
		return nil, fmt.Errorf("user client is not initialized")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "X-Auth-Jwt-Type", "service")
	serviceClaim, err := json.Marshal(ServiceClaim{
		ServiceName: "app-center",
		FuncName:    "getUserInfoFromAuthCenter",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal service claim: %v", err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "X-Auth-Service-Claim", string(serviceClaim))

	req := &user.GetProfileRequest{
		UserId: &uid,
		Keys:   keys,
	}

	resp, err := u.userClient.GetProfile(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	if resp.GetCode()/100 != 2 {
		return nil, fmt.Errorf("auth center returned code=%d, message=%s", resp.GetCode(), resp.GetMessage())
	}
	ud := resp.GetData()
	ui := &UserProfile{
		UserId:    ud.GetUserId(),
		Email:     ud.GetEmail(),
		CreatedAt: ud.GetCreatedAt().AsTime(),
		UpdatedAt: ud.GetUpdatedAt().AsTime(),
		Attrs:     ud.GetAttrs(),
	}
	return ui, nil
}
