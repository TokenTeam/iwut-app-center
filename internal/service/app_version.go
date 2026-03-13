package service

import (
	"context"
	"iwut-app-center/api/gen/go/app_center/v1/app_version"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/util"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AppVersionService struct {
	app_version.UnimplementedAppVersionServer
	appVersionUsecase *biz.AppVersionUsecase
	appUsecase        *biz.AppUsecase
	jwtUtil           *util.JwtUtil
}

func NewAppVersionService(appVersionUsecase *biz.AppVersionUsecase, appUsecase *biz.AppUsecase, jwtUtil *util.JwtUtil) *AppVersionService {
	return &AppVersionService{
		appVersionUsecase: appVersionUsecase,
		appUsecase:        appUsecase,
		jwtUtil:           jwtUtil,
	}
}

func (s *AppVersionService) GetAppVersionInfo(ctx context.Context, in *app_version.GetAppVersionInfoRequest) (*app_version.GetAppVersionInfoReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app_version.GetAppVersionInfoReply]("GetAppVersionInfo", nil)

	_, err := s.jwtUtil.GetServiceClaims(ctx)
	if err != nil {
		_, err := s.jwtUtil.GetBaseAuthClaims(ctx)
		if err != nil {
			return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
		}
	}
	versionInfo, err := s.appVersionUsecase.Repo.GetApplicationVersionInfo(ctx, in.GetClientId(), in.GetVersion())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if versionInfo == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application version without error"))
	}
	return successProcess(ctx, func(reqId string) *app_version.GetAppVersionInfoReply {
		return &app_version.GetAppVersionInfoReply{
			Code:    200,
			Message: "Get application version info successfully",
			Data: &app_version.ApplicationVersion{
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

func (s *AppVersionService) GetAppVersionInfoWithUserCheck(ctx context.Context, in *app_version.GetAppVersionInfoWithUserCheckRequest) (*app_version.GetAppVersionInfoWithUserCheckReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app_version.GetAppVersionInfoWithUserCheckReply]("GetAppVersionInfoWithUserCheck", nil)

	_, err := s.jwtUtil.GetServiceClaims(ctx)
	if err != nil {
		_, err := s.jwtUtil.GetBaseAuthClaims(ctx)
		if err != nil {
			return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
		}
	}
	allowed, versionInfo, err := s.appVersionUsecase.Repo.GetApplicationVersionInfoWithUserCheck(ctx, in.GetClientId(), in.GetVersion(), in.GetUserId())
	if err != nil {
		return nil, errorProcess(ctx, err)
	}
	if versionInfo == nil {
		return nil, errorProcess(ctx, errors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), "got nil application version without error"))
	}
	return successProcess(ctx, func(reqId string) *app_version.GetAppVersionInfoWithUserCheckReply {
		return &app_version.GetAppVersionInfoWithUserCheckReply{
			Code:    200,
			Message: "Get application version info with user check successfully",
			Data: &app_version.GetAppVersionInfoWithUserCheckReply_GetAppVersionInfoWithUserCheckReplyData{
				Allowed: allowed,
				AppVersion: &app_version.ApplicationVersion{
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

func (s *AppVersionService) CreateAppVersion(ctx context.Context, in *app_version.CreateAppVersionRequest) (*app_version.CreateAppVersionReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app_version.CreateAppVersionReply]("CreateAppVersion", nil)

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
	versionInfo, err := s.appVersionUsecase.Repo.CreateAppVersion(ctx, biz.ApplicationVersionInfo{
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
	return successProcess(ctx, func(reqId string) *app_version.CreateAppVersionReply {
		return &app_version.CreateAppVersionReply{
			Code:    200,
			Message: "Create application version successfully",
			Data: &app_version.CreateAppVersionReply_CreateAppVersionReplyData{
				ClientId:        versionInfo.ClientId,
				InternalVersion: versionInfo.InternalVersion,
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}
