package domain

const InitialDeveloperApplicationQuotaLimit int32 = 10

// DeveloperApplicationQuota constrains the number of applications currently
// owned by one administrator.
type DeveloperApplicationQuota struct {
	adminID   AuthID
	limit     int32
	usedCount int32
}

func NewDeveloperApplicationQuota(
	adminID AuthID,
	limit int32,
	usedCount int32,
) (DeveloperApplicationQuota, error) {
	if !adminID.valid() || limit < 0 || usedCount < 0 || usedCount > limit {
		return DeveloperApplicationQuota{}, NewInternalError(nil)
	}

	return DeveloperApplicationQuota{
		adminID:   adminID,
		limit:     limit,
		usedCount: usedCount,
	}, nil
}

func (quota DeveloperApplicationQuota) AdminID() AuthID {
	return quota.adminID
}

func (quota DeveloperApplicationQuota) Limit() int32 {
	return quota.limit
}

func (quota DeveloperApplicationQuota) UsedCount() int32 {
	return quota.usedCount
}

func (quota DeveloperApplicationQuota) HasCapacity() bool {
	return quota.usedCount < quota.limit
}

// Consume returns a new quota value so a failed persistence operation cannot
// partially mutate a caller's in-memory value.
func (quota DeveloperApplicationQuota) Consume() (DeveloperApplicationQuota, error) {
	if !quota.HasCapacity() {
		return DeveloperApplicationQuota{}, ErrApplicationQuotaExceeded
	}
	quota.usedCount++
	return quota, nil
}
