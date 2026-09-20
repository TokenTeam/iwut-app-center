package auth

import (
	"context"
	"errors"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

// ReviewScopeCatalog reuses the authoritative bounded cache while exposing the
// review capability's narrow port. It does not introduce another catalog.
type ReviewScopeCatalog struct {
	cache *ScopeCatalogCache
}

func NewReviewScopeCatalog(cache *ScopeCatalogCache) *ReviewScopeCatalog {
	return &ReviewScopeCatalog{cache: cache}
}

func (catalog *ReviewScopeCatalog) EnsureAllRequestable(
	ctx context.Context,
	scopes []reviewdomain.ScopeName,
) (reviewdomain.ScopeCatalogRevision, error) {
	if catalog == nil || catalog.cache == nil {
		return 0, reviewport.ErrScopeCatalogUnavailable
	}
	values := make([]versiondomain.ScopeName, len(scopes))
	for index, scope := range scopes {
		values[index] = versiondomain.ScopeName(scope)
	}
	revision, err := catalog.cache.EnsureAllRequestable(ctx, values)
	if err != nil {
		switch {
		case errors.Is(err, versionport.ErrScopeNotRequestable):
			return reviewdomain.ScopeCatalogRevision(revision), reviewport.ErrScopeNotRequestable
		case errors.Is(err, versionport.ErrScopeCatalogUnavailable):
			return 0, errors.Join(reviewport.ErrScopeCatalogUnavailable, err)
		default:
			return 0, err
		}
	}
	return reviewdomain.ScopeCatalogRevision(revision), nil
}

var _ reviewport.ScopeCatalog = (*ReviewScopeCatalog)(nil)
