package auth

import (
	"context"
	"errors"

	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

// PublicationScopeCatalog reuses the authoritative bounded cache while exposing the
// publication capability's narrow port. It does not introduce another catalog.
type PublicationScopeCatalog struct {
	cache *ScopeCatalogCache
}

func NewPublicationScopeCatalog(cache *ScopeCatalogCache) *PublicationScopeCatalog {
	return &PublicationScopeCatalog{cache: cache}
}

func (catalog *PublicationScopeCatalog) EnsureAllRequestable(
	ctx context.Context,
	scopes []publicationdomain.ScopeName,
) (publicationdomain.ScopeCatalogRevision, error) {
	if catalog == nil || catalog.cache == nil {
		return 0, publicationport.ErrScopeCatalogUnavailable
	}
	values := make([]versiondomain.ScopeName, len(scopes))
	for index, scope := range scopes {
		values[index] = versiondomain.ScopeName(scope)
	}
	revision, err := catalog.cache.EnsureAllRequestable(ctx, values)
	if err != nil {
		switch {
		case errors.Is(err, versionport.ErrScopeNotRequestable):
			return publicationdomain.ScopeCatalogRevision(revision), publicationport.ErrScopeNotRequestable
		case errors.Is(err, versionport.ErrScopeCatalogUnavailable):
			return 0, errors.Join(publicationport.ErrScopeCatalogUnavailable, err)
		default:
			return 0, err
		}
	}
	return publicationdomain.ScopeCatalogRevision(revision), nil
}

var _ publicationport.ScopeCatalog = (*PublicationScopeCatalog)(nil)
