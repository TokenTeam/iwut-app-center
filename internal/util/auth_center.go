package util

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/api/gen/go/auth_center/v1/auth"
	"iwut-app-center/api/gen/go/auth_center/v1/user"
	"iwut-app-center/internal/conf"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
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
	Attrs     map[string]any
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

func (u *AuthCenterUtil) GetUserClaimByUid(ctx context.Context, uid string, keys []string) (map[string]any, error) {
	l := log.NewHelper(log.WithContext(ctx, u.log.Logger()))
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	l.Debugf("GetUserClaimByUid uid: %s", uid)

	return u.getUserClaimFromAuthCenter(ctx, uid, keys)
}

type ServiceClaim struct {
	ServiceName string `json:"ServiceName"`
	FuncName    string `json:"FuncName"`
}

func (u *AuthCenterUtil) getUserInfoFromAuthCenter(ctx context.Context, uid string, keys []string) (*UserProfile, error) {
	l := log.NewHelper(log.WithContext(ctx, u.log.Logger()))
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
	ud, err := tailProcess(resp, err)
	if err != nil {
		return nil, err
	}
	if ud == nil {
		return nil, fmt.Errorf("failed to get user profile from auth center service")
	}
	attrs := make(map[string]any)
	for key, value := range ud.GetAttrs().GetFields() {
		attrs[key] = value.GetStringValue()
		if attrs[key] == "" {
			l.Warnf("attrs[%s] is empty\n", key)
		}
	}
	ui := &UserProfile{
		UserId:    ud.GetUserId(),
		Email:     ud.GetEmail(),
		CreatedAt: ud.GetCreatedAt().AsTime(),
		UpdatedAt: ud.GetUpdatedAt().AsTime(),
		Attrs:     attrs,
	}
	return ui, nil
}

func (u *AuthCenterUtil) getUserClaimFromAuthCenter(ctx context.Context, uid string, keys []string) (map[string]any, error) {
	if u.authClient == nil {
		return nil, fmt.Errorf("auth client is not initialized")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "X-Auth-Jwt-Type", "service")
	serviceClaim, err := json.Marshal(ServiceClaim{
		ServiceName: "app-center",
		FuncName:    "getUserClaimFromAuthCenter",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal service claim: %v", err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "X-Auth-Service-Claim", string(serviceClaim))
	req := &user.GetClaimsRequest{
		UserId: &uid,
		Keys:   keys,
	}

	resp, err := u.userClient.GetClaims(ctx, req)
	cd, err := tailProcess(resp, err)
	if err != nil {
		return nil, err
	}
	if cd == nil {
		return nil, fmt.Errorf("failed to get user claim from auth center service")
	}
	attrs := cd.AsMap()
	for key := range attrs {
		if !IsASCIIAlphaNumDashUnderscore(key) {
			return nil, errors.BadRequest(string(v1.ErrorReason_INVALID_KEY_NAME), fmt.Sprintf("invalid key: %s", key))
		}
	}

	return attrs, nil
}

type authCenterReply[N any] interface {
	*user.GetProfileReply |
		*user.GetClaimsReply

	GetCode() int32
	GetMessage() string
	GetData() *N
}

func tailProcess[N any, T authCenterReply[N]](resp T, err error) (*N, error) {
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("response is nil")
	}
	if resp.GetCode()/100 != 2 {
		return nil, fmt.Errorf("app center returned code=%d message=%s", resp.GetCode(), resp.GetMessage())
	}
	return resp.GetData(), nil
}
