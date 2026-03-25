package service

import (
	"context"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"
	"iwut-app-center/api/gen/go/app_center/v1/tester"
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/util"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
)

type TesterService struct {
	tester.UnimplementedTesterServer
	uc      *biz.TesterUsecase
	jwtUtil *util.JwtUtil
}

func NewTesterService(uc *biz.TesterUsecase, jwtUtil *util.JwtUtil) *TesterService {
	return &TesterService{uc: uc, jwtUtil: jwtUtil}
}

func (s *TesterService) GetTestLink(ctx context.Context, in *tester.GetTestLinkRequest) (*tester.GetTestLinkReply, error) {
	successProcess, errorProcess := util.GetProcesses[*tester.GetTestLinkReply]()

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}

	testLink, err := s.uc.GetTestLink(ctx, in.GetClientId(), in.GetVersion(), claim.Uid, time.Duration(in.GetExpireTime())*time.Second)
	if err != nil {
		return nil, errorProcess(ctx, err)
	}

	return successProcess(ctx, func(reqId string) *tester.GetTestLinkReply {
		return &tester.GetTestLinkReply{Code: 200, Message: "Get test link successfully", TraceId: reqId, Data: &tester.GetTestLinkReply_GetTaskLinkData{TestLink: testLink}}
	}), nil
}

func (s *TesterService) AddTester(ctx context.Context, in *tester.AddTesterRequest) (*tester.AddTesterReply, error) {
	successProcess, errorProcess := util.GetProcesses[*tester.AddTesterReply]()

	claim, err := s.jwtUtil.GetBaseAuthClaims(ctx)
	if err != nil {
		return nil, errorProcess(ctx, errors.Unauthorized(string(v1.ErrorReason_INVALID_JWT), "invalid JWT token: "+err.Error()))
	}

	if err := s.uc.AddTester(ctx, in.GetInviteId(), claim.Uid); err != nil {
		return nil, errorProcess(ctx, err)
	}

	return successProcess(ctx, func(reqId string) *tester.AddTesterReply {
		return &tester.AddTesterReply{Code: 200, Message: "Add tester successfully", TraceId: reqId}
	}), nil
}
