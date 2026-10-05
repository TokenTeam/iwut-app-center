package domain

import (
	"errors"
	"regexp"
	"time"
	"unicode/utf8"
)

type Purpose int32

const (
	Withdrawal Purpose = 1
	Closure    Purpose = 2
)

type Decision int32

const (
	Pending   Decision = 1
	Committed Decision = 2
	Cancelled Decision = 3
)

type Cleanup int32

const (
	NotRequired    Cleanup = 1
	CleanupPending Cleanup = 2
	Complete       Cleanup = 3
)

var (
	ErrInvalid     = errors.New("invalid account owner exit request")
	ErrConflict    = errors.New("account owner exit conflict")
	ErrNotFound    = errors.New("account owner exit not found")
	ErrUnavailable = errors.New("account owner exit unavailable")
)
var operationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var receiptPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Key struct{ AuthID, OperationID string }

func (k Key) Valid() bool {
	return k.AuthID != "" && len(k.AuthID) <= 200 && utf8.ValidString(k.AuthID) && operationPattern.MatchString(k.OperationID)
}

type Prepare struct {
	Key
	Purpose Purpose
}

func (p Prepare) Valid() bool {
	return p.Key.Valid() && (p.Purpose == Withdrawal || p.Purpose == Closure)
}

type Finish struct {
	Prepare
	ReceiptID string
	Decision  Decision
}

func (f Finish) Valid() bool {
	return f.Prepare.Valid() && (f.Decision == Cancelled && (f.ReceiptID == "" || receiptPattern.MatchString(f.ReceiptID)) || f.Decision == Committed && receiptPattern.MatchString(f.ReceiptID))
}

type Status struct {
	Prepare
	ReceiptID string
	Decision  Decision
	Cleanup   Cleanup
}
type Preparation struct {
	ReceiptID string
	Blocked   bool
}
type Work struct {
	Status
	Attempt       int
	NextAttemptAt time.Time
}
