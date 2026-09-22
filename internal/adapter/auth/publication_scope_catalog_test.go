package auth

import (
	"context"
	"errors"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	versiondomain "iwut-app-center/internal/version/domain"
	"testing"
	"time"
)

func TestPublicationScopeCatalog_BRPUB008SharedCacheAndFailClosed(t *testing.T) {
	clock := &fakeScopeCatalogClock{now: time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 19, RequestableScopes: []versiondomain.ScopeName{"read"}}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)
	catalog := NewPublicationScopeCatalog(cache)
	revision, err := catalog.EnsureAllRequestable(context.Background(), []publicationdomain.ScopeName{"read"})
	if err != nil || revision != 19 {
		t.Fatalf("%v %v", revision, err)
	}
	if _, err = NewReviewScopeCatalog(cache).EnsureAllRequestable(context.Background(), nil); err != nil || source.Calls() != 1 {
		t.Fatalf("cache not shared: %v", err)
	}
	if _, err = catalog.EnsureAllRequestable(context.Background(), []publicationdomain.ScopeName{"denied"}); !errors.Is(err, publicationport.ErrScopeNotRequestable) {
		t.Fatalf("%v", err)
	}
	clock.Advance(time.Minute)
	source.Set(ScopeCatalogSnapshot{}, errors.New("provider unavailable"))
	if _, err = catalog.EnsureAllRequestable(context.Background(), []publicationdomain.ScopeName{"read"}); !errors.Is(err, publicationport.ErrScopeCatalogUnavailable) {
		t.Fatalf("stale snapshot used: %v", err)
	}
}
