package domain

import "iwut-app-center/internal/shared"

type ApplicationVersionID string

func (id ApplicationVersionID) String() string { return string(id) }
func (id ApplicationVersionID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }
