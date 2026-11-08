package port

import (
	"context"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type IncomingApplicationAdminTransferRepository interface {
	ListIncoming(context.Context, shared.AuthID, domain.IncomingTransferQuery, time.Time) (domain.IncomingApplicationAdminTransferPage, error)
}
