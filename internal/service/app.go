package service

import (
	"context"
	"iwut-app-center/api/gen/go/app_center/v1/app"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/util"

	"github.com/go-kratos/kratos/v2/errors"
)

type AppService struct {
	app.UnimplementedAppServer
	appUsecase *biz.AppUsecase
}

func NewAppService(appUsecase *biz.AppUsecase) *AppService {
	return &AppService{
		appUsecase: appUsecase,
	}
}

func (s *AppService) GetApplicationInfo(ctx context.Context, in *app.GetApplicationInfoRequest) (*app.GetApplicationInfoReply, error) {
	successProcess, errorProcess := util.GetProcesses[*app.GetApplicationInfoReply]("GetApplicationInfo", nil)

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
				ClientId:       application.ClientId,
				ClientSecret:   application.ClientSecret,
				StableVersion:  application.StableVersion,
				GrayVersion:    application.GrayVersion,
				BetaVersion:    application.BetaVersion,
				GrayPercentage: application.GrayPercentage,
				Name:           application.Name,
				Status:         application.Status,
				Admin:          application.Admin,
				Collaborators:  application.Collaborators,
				Id:             application.Id(),
			},
			TraceId: reqId,
		}
	}, util.Audit{}), nil
}
