package shared

import "errors"

// ErrAccountExitBlocked means a persistent account lifecycle fence forbids new state.
var ErrAccountExitBlocked = errors.New("account owner exit blocks this operation")
