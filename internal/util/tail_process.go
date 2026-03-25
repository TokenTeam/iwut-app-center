package util

import (
	"context"
	"errors"
	v1 "iwut-app-center/api/gen/go/app_center/v1/error_reason"

	kratosErrors "github.com/go-kratos/kratos/v2/errors"
)

func GetErrorProcess() func(ctx context.Context, err error) error {
	return func(ctx context.Context, err error) error {
		traceID := RequestIDFrom(ctx)
		var errorMessage string
		var e *kratosErrors.Error
		if errors.As(err, &e) {
			if e.Metadata == nil {
				e.Metadata = map[string]string{}
			}
			e.Metadata["traceId"] = traceID
			errorMessage = e.Message
		} else {
			errorMessage = err.Error()
			errNew := kratosErrors.InternalServer(string(v1.ErrorReason_UNKNOWN_ERROR), errorMessage)
			errNew.Metadata = map[string]string{"traceId": traceID}
			err = errNew
		}
		return err
	}
}

func GetSuccessProcess[T any]() func(ctx context.Context, setReqId func(reqId string) T) T {
	return func(ctx context.Context, f func(reqId string) T) T {
		traceID := RequestIDFrom(ctx)
		return f(traceID)
	}
}

func GetProcesses[T any]() (func(ctx context.Context, setReqId func(reqId string) T) T, func(ctx context.Context, err error) error) {
	return GetSuccessProcess[T](), GetErrorProcess()
}

type UserInfoValue struct {
	ClientID string
	UserID   string
}
