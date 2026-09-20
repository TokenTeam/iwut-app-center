package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

var ErrInvalidScopeCatalogCacheConfiguration = errors.New("invalid scope catalog cache configuration")

type ScopeCatalogSnapshot struct {
	Revision          port.ScopeCatalogRevision
	GeneratedAt       time.Time
	RequestableScopes []domain.ScopeName
}

// ScopeCatalogSnapshotSource is the narrow boundary a future Auth transport
// adapter must implement. It deliberately does not define Auth's wire format or
// a complete ScopeDefinition model.
type ScopeCatalogSnapshotSource interface {
	FetchScopeCatalogSnapshot(ctx context.Context) (ScopeCatalogSnapshot, error)
}

type ScopeCatalogCache struct {
	source ScopeCatalogSnapshotSource
	clock  port.Clock
	ttl    time.Duration

	mu       sync.RWMutex
	snapshot *cachedScopeCatalogSnapshot
	refresh  singleflight.Group
}

type cachedScopeCatalogSnapshot struct {
	revision          port.ScopeCatalogRevision
	generatedAt       time.Time
	requestableScopes map[domain.ScopeName]struct{}
	expiresAt         time.Time
}

func NewScopeCatalogCache(
	source ScopeCatalogSnapshotSource,
	clock port.Clock,
	ttl time.Duration,
) (*ScopeCatalogCache, error) {
	if source == nil || clock == nil || ttl <= 0 {
		return nil, ErrInvalidScopeCatalogCacheConfiguration
	}
	return &ScopeCatalogCache{source: source, clock: clock, ttl: ttl}, nil
}

func (cache *ScopeCatalogCache) EnsureAllRequestable(
	ctx context.Context,
	scopes []domain.ScopeName,
) (port.ScopeCatalogRevision, error) {
	if cache == nil {
		return 0, port.ErrScopeCatalogUnavailable
	}

	snapshot, err := cache.currentSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	for _, scope := range scopes {
		if _, exists := snapshot.requestableScopes[scope]; !exists {
			return snapshot.revision, port.ErrScopeNotRequestable
		}
	}
	return snapshot.revision, nil
}

func (cache *ScopeCatalogCache) currentSnapshot(ctx context.Context) (*cachedScopeCatalogSnapshot, error) {
	now := cache.clock.Now()
	if snapshot := cache.freshSnapshot(now); snapshot != nil {
		return snapshot, nil
	}

	value, err, _ := cache.refresh.Do("scope-catalog", func() (any, error) {
		now := cache.clock.Now()
		if snapshot := cache.freshSnapshot(now); snapshot != nil {
			return snapshot, nil
		}

		loaded, loadErr := cache.source.FetchScopeCatalogSnapshot(ctx)
		if loadErr != nil {
			return nil, fmt.Errorf("%w: fetch Auth snapshot: %w", port.ErrScopeCatalogUnavailable, loadErr)
		}

		requestable := make(map[domain.ScopeName]struct{}, len(loaded.RequestableScopes))
		for _, scope := range loaded.RequestableScopes {
			requestable[scope] = struct{}{}
		}
		candidate := &cachedScopeCatalogSnapshot{
			revision:          loaded.Revision,
			generatedAt:       loaded.GeneratedAt,
			requestableScopes: requestable,
			expiresAt:         cache.clock.Now().Add(cache.ttl),
		}

		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.snapshot != nil && candidate.revision < cache.snapshot.revision {
			return nil, fmt.Errorf(
				"%w: Auth snapshot revision regressed from %d to %d",
				port.ErrScopeCatalogUnavailable,
				cache.snapshot.revision,
				candidate.revision,
			)
		}
		cache.snapshot = candidate
		return candidate, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*cachedScopeCatalogSnapshot), nil
}

func (cache *ScopeCatalogCache) freshSnapshot(now time.Time) *cachedScopeCatalogSnapshot {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.snapshot == nil || !now.Before(cache.snapshot.expiresAt) {
		return nil
	}
	return cache.snapshot
}

var _ port.ScopeCatalog = (*ScopeCatalogCache)(nil)
