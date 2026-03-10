package biz

import (
	"context"
	"time"
)

type TesterRepo interface {
	GetTestLink(parentCtx context.Context, clientId string, betaVersion int32, inviter string, expiration time.Duration) (string, error)
	AddTester(parentCtx context.Context, inviteId, userId string) error
}

type TesterUsecase struct {
	Repo TesterRepo
}

func NewTesterUsecase(repo TesterRepo) *TesterUsecase {
	return &TesterUsecase{Repo: repo}
}
