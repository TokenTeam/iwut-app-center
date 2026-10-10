package domain

import "time"

// Application is the aggregate root for stable application identity and
// current administration.
type Application struct {
	id                           ApplicationID
	name                         ApplicationName
	adminID                      AuthID
	lifecycleStatus              ApplicationLifecycleStatus
	lifecycleRevision            int64
	platformAvailabilityStatus   PlatformAvailabilityStatus
	platformAvailabilityRevision int64
	archiveStatus                ApplicationArchiveStatus
	archiveRevision              int64
	createdAt                    time.Time
}

type ApplicationLifecycleStatus string

type PlatformAvailabilityStatus string
type ApplicationArchiveStatus string

const (
	ApplicationLifecycleActive  ApplicationLifecycleStatus = "ACTIVE"
	ApplicationLifecycleClosing ApplicationLifecycleStatus = "CLOSING"
	ApplicationLifecycleClosed  ApplicationLifecycleStatus = "CLOSED"
)

const (
	ApplicationArchiveUnarchived ApplicationArchiveStatus = "UNARCHIVED"
	ApplicationArchiveArchived   ApplicationArchiveStatus = "ARCHIVED"
)

const (
	PlatformAvailabilityAvailable PlatformAvailabilityStatus = "AVAILABLE"
	PlatformAvailabilitySuspended PlatformAvailabilityStatus = "SUSPENDED"
)

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
		id:                           id,
		name:                         name,
		adminID:                      adminID,
		lifecycleStatus:              ApplicationLifecycleActive,
		lifecycleRevision:            1,
		platformAvailabilityStatus:   PlatformAvailabilityAvailable,
		platformAvailabilityRevision: 1,
		archiveStatus:                ApplicationArchiveUnarchived,
		archiveRevision:              1,
		createdAt:                    createdAt.UTC(),
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

func (application *Application) LifecycleStatus() ApplicationLifecycleStatus {
	return application.lifecycleStatus
}
func (application *Application) LifecycleRevision() int64 { return application.lifecycleRevision }

func (application *Application) PlatformAvailabilityStatus() PlatformAvailabilityStatus {
	return application.platformAvailabilityStatus
}
func (application *Application) PlatformAvailabilityRevision() int64 {
	return application.platformAvailabilityRevision
}

func (application *Application) ArchiveStatus() ApplicationArchiveStatus {
	return application.archiveStatus
}
func (application *Application) ArchiveRevision() int64 { return application.archiveRevision }

func RestoreApplication(id ApplicationID, name ApplicationName, adminID AuthID, status ApplicationLifecycleStatus, revision int64, availabilityStatus PlatformAvailabilityStatus, availabilityRevision int64, archiveStatus ApplicationArchiveStatus, archiveRevision int64, createdAt time.Time) (*Application, error) {
	if status != ApplicationLifecycleActive && status != ApplicationLifecycleClosing && status != ApplicationLifecycleClosed || revision < 1 ||
		(availabilityStatus != PlatformAvailabilityAvailable && availabilityStatus != PlatformAvailabilitySuspended) || availabilityRevision < 1 ||
		(archiveStatus != ApplicationArchiveUnarchived && archiveStatus != ApplicationArchiveArchived) || archiveRevision < 1 {
		return nil, NewInternalError(nil)
	}
	value, err := NewApplication(id, name, adminID, createdAt)
	if err != nil {
		return nil, err
	}
	value.lifecycleStatus, value.lifecycleRevision = status, revision
	value.platformAvailabilityStatus, value.platformAvailabilityRevision = availabilityStatus, availabilityRevision
	value.archiveStatus, value.archiveRevision = archiveStatus, archiveRevision
	return value, nil
}

func (application *Application) CreatedAt() time.Time {
	return application.createdAt
}
