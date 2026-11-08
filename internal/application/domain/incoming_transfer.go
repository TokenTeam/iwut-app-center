package domain

import (
	"errors"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

var (
	ErrIncomingTransferIdentityRequired = errors.New("user identity is required")
	ErrInvalidIncomingTransferQuery     = errors.New("incoming application transfer query is invalid")
	ErrInvalidIncomingTransferPageToken = errors.New("incoming application transfer page token is invalid")
	ErrIncomingTransferUnavailable      = errors.New("incoming application transfer query is unavailable")
)

type IncomingTransferQuery struct {
	PageSize  int32
	PageToken string
}

func NewIncomingTransferQuery(size int32, token string) (IncomingTransferQuery, error) {
	if size == 0 {
		size = 20
	}
	if size < 1 || size > 100 || len(token) > 4096 || !utf8.ValidString(token) {
		return IncomingTransferQuery{}, ErrInvalidIncomingTransferQuery
	}
	return IncomingTransferQuery{size, token}, nil
}

type IncomingApplicationAdminTransfer struct {
	TransferID      ApplicationAdminTransferID
	ApplicationID   shared.ApplicationID
	ApplicationName string
	FromAuthID      shared.AuthID
	ToAuthID        shared.AuthID
	Status          ApplicationAdminTransferStatus
	RequestedAt     time.Time
	ExpiresAt       time.Time
}

func (i IncomingApplicationAdminTransfer) Validate(caller shared.AuthID, at time.Time) error {
	name, err := NewApplicationName(i.ApplicationName)
	if !i.TransferID.IsValid() || !i.ApplicationID.IsValid() || err != nil || name.String() != i.ApplicationName || !i.FromAuthID.IsValid() || !i.ToAuthID.IsValid() || i.ToAuthID != caller || i.FromAuthID == caller || i.Status != ApplicationAdminTransferPending || !incomingTransferTime(i.RequestedAt) || !incomingTransferTime(i.ExpiresAt) || !i.ExpiresAt.Equal(i.RequestedAt.Add(ApplicationAdminTransferLifetime)) || !i.ExpiresAt.After(at) {
		return ErrApplicationAdminTransferStateInconsistent
	}
	return nil
}
func incomingTransferTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Equal(t.Truncate(time.Millisecond))
}

type IncomingApplicationAdminTransferPage struct {
	Items         []IncomingApplicationAdminTransfer
	NextPageToken string
	AsOf          time.Time
}

func (p IncomingApplicationAdminTransferPage) Validate(caller shared.AuthID, q IncomingTransferQuery, at time.Time) error {
	if p.Items == nil || len(p.Items) > int(q.PageSize) || !p.AsOf.Equal(at) || !incomingTransferTime(p.AsOf) || len(p.NextPageToken) > 4096 || !utf8.ValidString(p.NextPageToken) || (len(p.Items) == 0 && p.NextPageToken != "") {
		return ErrApplicationAdminTransferStateInconsistent
	}
	seenTransfers := make(map[ApplicationAdminTransferID]bool, len(p.Items))
	seenApps := make(map[shared.ApplicationID]bool, len(p.Items))
	for n, item := range p.Items {
		if seenTransfers[item.TransferID] || seenApps[item.ApplicationID] {
			return ErrApplicationAdminTransferStateInconsistent
		}
		seenTransfers[item.TransferID], seenApps[item.ApplicationID] = true, true
		if err := item.Validate(caller, at); err != nil {
			return err
		}
		if n > 0 {
			prev := p.Items[n-1]
			if prev.RequestedAt.Before(item.RequestedAt) || (prev.RequestedAt.Equal(item.RequestedAt) && prev.TransferID.String() <= item.TransferID.String()) {
				return ErrApplicationAdminTransferStateInconsistent
			}
		}
	}
	return nil
}
