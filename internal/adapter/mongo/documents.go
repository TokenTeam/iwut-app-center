package mongo

import (
	"errors"
	"fmt"
	"time"

	"iwut-app-center/internal/application/domain"
)

var errCorruptApplicationDocument = errors.New("corrupt application document")

type applicationDocument struct {
	ID                          string    `bson:"id"`
	Name                        string    `bson:"name"`
	NameKey                     string    `bson:"nameKey"`
	AdminID                     string    `bson:"adminId"`
	CreatedAt                   time.Time `bson:"createdAt"`
	NextVersionSequence         int32     `bson:"nextVersionSequence"`
	NextProfileRevisionSequence int32     `bson:"nextProfileRevisionSequence"`
}

type applicationCreationQuotaDocument struct {
	AdminID   string    `bson:"adminId"`
	Limit     int32     `bson:"limit"`
	UsedCount int32     `bson:"usedCount"`
	Revision  int64     `bson:"revision"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

func applicationToDocument(application *domain.Application) (applicationDocument, error) {
	if application == nil {
		return applicationDocument{}, fmt.Errorf("map application document: application is nil")
	}

	return applicationDocument{
		ID:                          application.ID().String(),
		Name:                        application.Name().String(),
		NameKey:                     application.Name().Key(),
		AdminID:                     application.AdminID().String(),
		CreatedAt:                   application.CreatedAt().UTC(),
		NextVersionSequence:         1,
		NextProfileRevisionSequence: 1,
	}, nil
}

// applicationFromDocument is deliberately kept inside the persistence
// adapter. A malformed stored document is corruption, not caller validation.
func applicationFromDocument(document applicationDocument) (*domain.Application, error) {
	if document.NextVersionSequence < 1 || document.NextProfileRevisionSequence < 1 {
		return nil, fmt.Errorf("%w: invalid next sequence", errCorruptApplicationDocument)
	}

	id, err := domain.ParseApplicationID(document.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid application ID: %v", errCorruptApplicationDocument, err)
	}
	name, err := domain.NewApplicationName(document.Name)
	if err != nil || name.Key() != document.NameKey {
		return nil, fmt.Errorf("%w: invalid application name", errCorruptApplicationDocument)
	}
	adminID, err := domain.NewAuthID(document.AdminID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid admin ID", errCorruptApplicationDocument)
	}
	application, err := domain.NewApplication(id, name, adminID, document.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid application fields", errCorruptApplicationDocument)
	}
	return application, nil
}
