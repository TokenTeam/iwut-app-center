package domain

import "time"

// Application is the aggregate root for stable application identity and
// current administration.
type Application struct {
	id        ApplicationID
	name      ApplicationName
	adminID   AuthID
	createdAt time.Time
}

func NewApplication(
	id ApplicationID,
	name ApplicationName,
	adminID AuthID,
	createdAt time.Time,
) (*Application, error) {
	if !id.IsValid() || !name.valid() || !adminID.IsValid() || createdAt.IsZero() {
		return nil, NewInternalError(nil)
	}

	return &Application{
		id:        id,
		name:      name,
		adminID:   adminID,
		createdAt: createdAt.UTC(),
	}, nil
}

func (application *Application) ID() ApplicationID {
	return application.id
}

func (application *Application) Name() ApplicationName {
	return application.name
}

func (application *Application) AdminID() AuthID {
	return application.adminID
}

func (application *Application) CreatedAt() time.Time {
	return application.createdAt
}
