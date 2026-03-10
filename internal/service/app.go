package service

import (
	"context"
	"iwut-app-center/api/gen/go/app_center/v1/app"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/util"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AppService struct {
	app.UnimplementedAppServer
	appUsecase *biz.AppUsecase
	jwtUtil    *util.JwtUtil
}

func NewAppService(appUsecase *biz.AppUsecase, jwtUtil *util.JwtUtil) *AppService {
	return &AppService{
		appUsecase: appUsecase,
		jwtUtil:    jwtUtil,
	}
}

func (s *AppService) GetApplicationInfo(ctx context.Context, in *app.GetApplicationInfoRequest) (*app.GetApplicationInfoReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.GetApplicationInfoReply]("GetApplicationInfo", nil)
	isUserToken := false
	_, err := s.jwtUtil.GetServiceClaims(ctx)
	if err != nil {
		_, err := s.jwtUtil.GetBaseAuthClaims(ctx)
		// 从一方面看 这个错误可能是因为调用方没有设置service 相关数据导致的 但是 我不想让外部用户看到如：”缺少服务调用指示的错误“
		if err != nil {
			return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
		}
		isUserToken = true
	}
	application, err := s.appUsecase.Repo.GetApplicationInfo(ctx, in.GetClientId())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if application == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application without error"))
	}
	return successProcess(ctx, func(reqId string) *app.GetApplicationInfoReply {
		return &app.GetApplicationInfoReply{
			Code:    200,
			Message: "Get application info successfully",
			Data: &app.GetApplicationInfoReply_Application{
				ClientId: application.ClientId,
				ClientSecret: func() string {
					if isUserToken {
						return ""
					}
					return application.ClientSecret
				}(),
				StableVersion:  application.StableVersion,
				GreyVersion:    application.GreyVersion,
				BetaVersion:    application.BetaVersion,
				GreyPercentage: application.GreyPercentage,
				Name:           application.Name,
				Status:         application.Status,
				Admin:          application.Admin,
				Collaborators:  application.Collaborators,
				RedirectUri:    application.RedirectUri,
				Id:             application.Id(),
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func (s *AppService) GetAppVersionInfo(ctx context.Context, in *app.GetAppVersionInfoRequest) (*app.GetAppVersionInfoReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.GetAppVersionInfoReply]("GetAppVersionInfo", nil)

	_, err := s.jwtUtil.GetServiceClaims(ctx)
	if err != nil {
		_, err := s.jwtUtil.GetBaseAuthClaims(ctx)
		if err != nil {
			return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
		}
	}
	versionInfo, err := s.appUsecase.Repo.GetApplicationVersionInfo(ctx, in.GetClientId(), in.GetVersion())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if versionInfo == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application version without error"))
	}
	return successProcess(ctx, func(reqId string) *app.GetAppVersionInfoReply {
		return &app.GetAppVersionInfoReply{
			Code:    200,
			Message: "Get application version info successfully",
			Data: &app.ApplicationVersion{
				ClientId:        versionInfo.ClientId,
				InternalVersion: versionInfo.InternalVersion,
				Version:         versionInfo.Version,
				BasicScope:      versionInfo.BasicScope,
				OptionalScope:   versionInfo.OptionalScope,
				DisplayName:     versionInfo.DisplayName,
				Description:     versionInfo.Description,
				Url:             versionInfo.Url,
				Icon:            versionInfo.Icon,
				Status:          versionInfo.Status,
				CreatedAt:       timestamppb.New(versionInfo.CreatedAt),
				DeletedAt: func() *timestamppb.Timestamp {
					if versionInfo.DeletedAt != nil {
						return timestamppb.New(*versionInfo.DeletedAt)
					}
					return nil
				}(),
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func (s *AppService) GetAppVersionInfoWithUserCheck(ctx context.Context, in *app.GetAppVersionInfoWithUserCheckRequest) (*app.GetAppVersionInfoWithUserCheckReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.GetAppVersionInfoWithUserCheckReply]("GetAppVersionInfoWithUserCheck", nil)

	_, err := s.jwtUtil.GetServiceClaims(ctx)
	if err != nil {
		_, err := s.jwtUtil.GetBaseAuthClaims(ctx)
		if err != nil {
			return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
		}
	}
	allowed, versionInfo, err := s.appUsecase.Repo.GetApplicationVersionInfoWithUserCheck(ctx, in.GetClientId(), in.GetVersion(), in.GetUserId())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if versionInfo == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application version without error"))
	}
	return successProcess(ctx, func(reqId string) *app.GetAppVersionInfoWithUserCheckReply {
		return &app.GetAppVersionInfoWithUserCheckReply{
			Code:    200,
			Message: "Get application version info with user check successfully",
			Data: &app.GetAppVersionInfoWithUserCheckReply_GetAppVersionInfoWithUserCheckReplyData{
				Allowed: allowed,
				AppVersion: &app.ApplicationVersion{
					ClientId:        versionInfo.ClientId,
					InternalVersion: versionInfo.InternalVersion,
					Version:         versionInfo.Version,
					BasicScope:      versionInfo.BasicScope,
					OptionalScope:   versionInfo.OptionalScope,
					DisplayName:     versionInfo.DisplayName,
					Description:     versionInfo.Description,
					Url:             versionInfo.Url,
					Icon:            versionInfo.Icon,
					Status:          versionInfo.Status,
					CreatedAt:       timestamppb.New(versionInfo.CreatedAt),
					DeletedAt: func() *timestamppb.Timestamp {
						if versionInfo.DeletedAt != nil {
							return timestamppb.New(*versionInfo.DeletedAt)
						}
						return nil
					}(),
				},
			},
			TraceId: reqId,
		}
	}), nil
}

func (s *AppService) GetAppList(ctx context.Context, _ *emptypb.Empty) (*app.GetAppListReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.GetAppListReply]("GetAppList", nil)

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}
	appList, err := s.appUsecase.Repo.GetAppList(ctx, claim.Uid)
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	return successProcess(ctx, func(reqId string) *app.GetAppListReply {
		replyData := make([]*app.GetAppListReply_ApplicationVersion, 0, len(appList))
		for _, appInfo := range appList {
			replyData = append(replyData, &app.GetAppListReply_ApplicationVersion{
				ClientId:        appInfo.ClientId,
				InternalVersion: appInfo.InternalVersion,
				Version:         appInfo.Version,
				BasicScope:      appInfo.BasicScope,
				OptionalScope:   appInfo.OptionalScope,
				DisplayName:     appInfo.DisplayName,
				Description:     appInfo.Description,
				Url:             appInfo.Url,
				Icon:            appInfo.Icon,
				Status:          appInfo.Status,
				CreatedAt:       timestamppb.New(appInfo.CreatedAt),
				Name:            appInfo.Name,
				Admin:           appInfo.Admin,
				Collaborators:   appInfo.Collaborators,
			})
		}
		return &app.GetAppListReply{
			Code:    200,
			Message: "Get application list successfully",
			Data:    replyData,
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}
func (s *AppService) CreateApp(ctx context.Context, in *app.CreateAppRequest) (*app.CreateAppReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.CreateAppReply]("CreateApp", nil)

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}
	if claim.DeveloperId == nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_NOT_A_DEVELOPER), "not a developer"))
	}
	application, err := s.appUsecase.Repo.CreateApplication(ctx, claim.Uid, in.GetName())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if application == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_IMPOSSIBLE_ERROR), "got nil application without error"))
	}
	return successProcess(ctx, func(reqId string) *app.CreateAppReply {
		return &app.CreateAppReply{
			Code:    200,
			Message: "Create application successfully",
			Data: &app.CreateAppReply_CreateAppReplyData{
				ClientId:     application.ClientId,
				ClientSecret: application.ClientSecret,
				Name:         application.Name,
				Admin:        application.Admin,
				Id:           application.Id(),
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func (s *AppService) CreateAppVersion(ctx context.Context, in *app.CreateAppVersionRequest) (*app.CreateAppVersionReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.CreateAppVersionReply]("CreateAppVersion", nil)

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}
	application, err := s.appUsecase.Repo.GetApplicationInfo(ctx, in.GetClientId())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if application == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application without error"))
	}
	if application.Admin != claim.Uid || lo.Contains(application.Collaborators, claim.Uid) {
		return nil, errorProcess(ctx, errors.BadRequest(string(v1.ErrorReason_PERMISSION_DENIED), "only admin and collaborators can create new version"))
	}
	versionInfo, err := s.appUsecase.Repo.CreateAppVersion(ctx, biz.ApplicationVersionInfo{
		ClientId:      in.GetClientId(),
		BasicScope:    in.GetBasicScope(),
		OptionalScope: in.GetOptionalScope(),
		Version:       in.GetVersion(),
		DisplayName:   in.GetDisplayName(),
		Description:   in.GetDescription(),
		Url:           in.GetUrl(),
		Icon:          in.GetIcon(),
	})
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	return successProcess(ctx, func(reqId string) *app.CreateAppVersionReply {
		return &app.CreateAppVersionReply{
			Code:    200,
			Message: "Create application version successfully",
			Data: &app.CreateAppVersionReply_CreateAppVersionReplyData{
				ClientId:        versionInfo.ClientId,
				InternalVersion: versionInfo.InternalVersion,
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func (s *AppService) UpdateAppRule(ctx context.Context, in *app.UpdateAppRuleRequest) (*app.UpdateAppRuleReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.UpdateAppRuleReply]("UpdateAppRule", nil)

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}
	err = s.appUsecase.Repo.UpdateApplicationRule(ctx, in.GetClientId(), claim.Uid, ruleConverter(in.GetRule()))
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	return successProcess(ctx, func(reqId string) *app.UpdateAppRuleReply {
		return &app.UpdateAppRuleReply{
			Code:    200,
			Message: "Update application rule successfully",
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func (s *AppService) UpdateAppRedirectUri(ctx context.Context, in *app.UpdateAppRedirectUriRequest) (*app.UpdateAppRedirectUriReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.UpdateAppRedirectUriReply]("UpdateAppRedirectUri", nil)

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}
	err = s.appUsecase.Repo.UpdateApplicationRedirectUri(ctx, in.GetClientId(), claim.Uid, in.GetRedirectUri())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	return successProcess(ctx, func(reqId string) *app.UpdateAppRedirectUriReply {
		return &app.UpdateAppRedirectUriReply{
			Code:    200,
			Message: "Update application redirect uri successfully",
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}

func ruleConverter(in *app.UpdateAppRuleRequest_Rule) *util.Rule {
	if in == nil {
		return nil
	}
	rule := &util.Rule{
		Operator:     in.GetOperator(),
		Field:        stringPtr(in.GetField()),
		DefaultField: stringPtr(in.GetDefaultField()),
		Value:        stringPtr(in.GetValue()),
	}
	if in.Rules != nil {
		rules := make([]util.Rule, 0, len(in.Rules))
		for _, rule := range in.Rules {
			rules = append(rules, *ruleConverter(rule))
		}
		rule.Rules = &rules
	}
	return rule
}

func stringPtr(s string) *string {
	return &s
}
