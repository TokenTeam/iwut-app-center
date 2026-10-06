package generator

import (
	"github.com/google/uuid"
	"iwut-app-center/internal/application/domain"
)

type ApplicationAdminTransferUUIDv7Generator struct{}

func NewApplicationAdminTransferUUIDv7Generator() *ApplicationAdminTransferUUIDv7Generator {
	return &ApplicationAdminTransferUUIDv7Generator{}
}

func (*ApplicationAdminTransferUUIDv7Generator) NewUUIDv7() (domain.ApplicationAdminTransferID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return domain.ApplicationAdminTransferID(id.String()), nil
}
