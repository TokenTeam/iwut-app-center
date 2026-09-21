package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type fakeScopeCatalogClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *fakeScopeCatalogClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *fakeScopeCatalogClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type fakeScopeCatalogSnapshotSource struct {
	mu       sync.Mutex
	snapshot ScopeCatalogSnapshot
	err      error
	calls    int
	started  chan struct{}
	release  <-chan struct{}
}

func (source *fakeScopeCatalogSnapshotSource) FetchScopeCatalogSnapshot(ctx context.Context) (ScopeCatalogSnapshot, error) {
	source.mu.Lock()
	source.calls++
	snapshot := source.snapshot
	err := source.err
	started := source.started
	release := source.release
	source.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ScopeCatalogSnapshot{}, ctx.Err()
		}
	}
	return snapshot, err
}

func (source *fakeScopeCatalogSnapshotSource) Set(snapshot ScopeCatalogSnapshot, err error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.snapshot = snapshot
	source.err = err
}

func (source *fakeScopeCatalogSnapshotSource) Calls() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

func newTestScopeCatalogCache(t *testing.T, source ScopeCatalogSnapshotSource, clock port.Clock, ttl time.Duration) *ScopeCatalogCache {
	t.Helper()
	cache, err := NewScopeCatalogCache(source, clock, ttl)
	if err != nil {
		t.Fatalf("NewScopeCatalogCache() error = %v", err)
	}
	return cache
}

func TestScopeCatalogCache_BR_VER_007_MissLoadsSnapshotAndTTLHitReusesIt(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Date(2026, time.September, 19, 1, 0, 0, 0, time.UTC)}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{
		Revision:          12,
		GeneratedAt:       clock.Now().Add(-time.Minute),
		RequestableScopes: []domain.ScopeName{"profile.basic", "schedule.read"},
	}}
	cache := newTestScopeCatalogCache(t, source, clock, 5*time.Minute)

	for _, scopes := range [][]domain.ScopeName{{"profile.basic"}, {"schedule.read"}} {
		revision, err := cache.EnsureAllRequestable(context.Background(), scopes)
		if err != nil || revision != 12 {
			t.Fatalf("EnsureAllRequestable(%v) = (%d, %v), want revision 12", scopes, revision, err)
		}
		clock.Advance(time.Minute)
	}
	if source.Calls() != 1 {
		t.Fatalf("source calls = %d, want one within TTL", source.Calls())
	}
}

func TestScopeCatalogCache_BR_VER_007_ExpiresAtExactTTLAndRefreshesSynchronously(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Date(2026, time.September, 19, 1, 0, 0, 0, time.UTC)}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 1, RequestableScopes: []domain.ScopeName{"profile.basic"}}}
	cache := newTestScopeCatalogCache(t, source, clock, 5*time.Minute)
	if _, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"}); err != nil {
		t.Fatalf("initial EnsureAllRequestable() error = %v", err)
	}

	clock.Advance(5 * time.Minute)
	source.Set(ScopeCatalogSnapshot{Revision: 2, RequestableScopes: []domain.ScopeName{"schedule.read"}}, nil)
	revision, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"schedule.read"})
	if err != nil || revision != 2 {
		t.Fatalf("refreshed EnsureAllRequestable() = (%d, %v), want revision 2", revision, err)
	}
	if source.Calls() != 2 {
		t.Fatalf("source calls = %d, want two", source.Calls())
	}
}

func TestScopeCatalogCache_BR_VER_007_RefreshFailureDoesNotUseStaleSnapshot(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Date(2026, time.September, 19, 1, 0, 0, 0, time.UTC)}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 3, RequestableScopes: []domain.ScopeName{"profile.basic"}}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)
	if _, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"}); err != nil {
		t.Fatalf("initial EnsureAllRequestable() error = %v", err)
	}

	clock.Advance(time.Minute)
	cause := errors.New("Auth unavailable")
	source.Set(ScopeCatalogSnapshot{}, cause)
	revision, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"})
	if revision != 0 || !errors.Is(err, port.ErrScopeCatalogUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("expired EnsureAllRequestable() = (%d, %v), want unavailable retaining cause", revision, err)
	}
}

func TestScopeCatalogCache_BR_VER_007_UnknownAndEmptyScopesReturnActualRevision(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Now()}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 23, RequestableScopes: []domain.ScopeName{"known"}}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)

	revision, err := cache.EnsureAllRequestable(context.Background(), nil)
	if err != nil || revision != 23 || source.Calls() != 1 {
		t.Fatalf("empty EnsureAllRequestable() = (%d, %v), calls %d; want actual revision and one load", revision, err, source.Calls())
	}
	revision, err = cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"unknown"})
	if revision != 23 || !errors.Is(err, port.ErrScopeNotRequestable) {
		t.Fatalf("unknown EnsureAllRequestable() = (%d, %v), want revision 23 and ScopeNotRequestable", revision, err)
	}
}

func TestScopeCatalogCache_BR_VER_007_DefensivelyCopiesSourceSnapshot(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Now()}
	scopes := []domain.ScopeName{"profile.basic"}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 1, RequestableScopes: scopes}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)
	if _, err := cache.EnsureAllRequestable(context.Background(), scopes); err != nil {
		t.Fatalf("initial EnsureAllRequestable() error = %v", err)
	}

	scopes[0] = "mutated"
	if _, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"}); err != nil {
		t.Fatalf("cached snapshot changed through source slice: %v", err)
	}
}

func TestScopeCatalogCache_BR_VER_007_ConcurrentRefreshUsesSingleflight(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Now()}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := &fakeScopeCatalogSnapshotSource{
		snapshot: ScopeCatalogSnapshot{Revision: 9, RequestableScopes: []domain.ScopeName{"profile.basic"}},
		started:  started,
		release:  release,
	}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)

	const callers = 32
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers)
	var done sync.WaitGroup
	done.Add(callers)
	var failures atomic.Int32
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			revision, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"})
			if err != nil || revision != 9 {
				failures.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	<-started
	close(release)
	done.Wait()

	if failures.Load() != 0 {
		t.Fatalf("concurrent callers failed = %d", failures.Load())
	}
	if source.Calls() != 1 {
		t.Fatalf("source calls = %d, want one singleflight refresh", source.Calls())
	}
}

func TestScopeCatalogCache_BR_VER_007_RevisionRegressionFailsClosed(t *testing.T) {
	t.Parallel()

	clock := &fakeScopeCatalogClock{now: time.Now()}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{Revision: 8, RequestableScopes: []domain.ScopeName{"profile.basic"}}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)
	if _, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"}); err != nil {
		t.Fatalf("initial EnsureAllRequestable() error = %v", err)
	}

	clock.Advance(time.Minute)
	source.Set(ScopeCatalogSnapshot{Revision: 7, RequestableScopes: []domain.ScopeName{"profile.basic"}}, nil)
	revision, err := cache.EnsureAllRequestable(context.Background(), []domain.ScopeName{"profile.basic"})
	if revision != 0 || !errors.Is(err, port.ErrScopeCatalogUnavailable) {
		t.Fatalf("regressed EnsureAllRequestable() = (%d, %v), want fail-closed unavailable", revision, err)
	}
}

func TestScopeCatalogCache_BR_SCP_003_SameRevisionMutationFailsClosed(t *testing.T) {
	t.Parallel()
	generatedAt := time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC)
	clock := &fakeScopeCatalogClock{now: time.Now()}
	source := &fakeScopeCatalogSnapshotSource{snapshot: ScopeCatalogSnapshot{
		Revision: 8, GeneratedAt: generatedAt, RequestableScopes: []domain.ScopeName{"profile.basic"},
	}}
	cache := newTestScopeCatalogCache(t, source, clock, time.Minute)
	if _, err := cache.EnsureAllRequestable(t.Context(), []domain.ScopeName{"profile.basic"}); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	clock.Advance(time.Minute)
	source.Set(ScopeCatalogSnapshot{
		Revision: 8, GeneratedAt: generatedAt, RequestableScopes: []domain.ScopeName{"schedule.read"},
	}, nil)
	if revision, err := cache.EnsureAllRequestable(t.Context(), []domain.ScopeName{"schedule.read"}); revision != 0 || !errors.Is(err, port.ErrScopeCatalogUnavailable) {
		t.Fatalf("EnsureAllRequestable() = (%d, %v), want fail-closed", revision, err)
	}
}

func TestNewScopeCatalogCache_RejectsInvalidWiring(t *testing.T) {
	t.Parallel()

	validSource := &fakeScopeCatalogSnapshotSource{}
	validClock := &fakeScopeCatalogClock{now: time.Now()}
	testCases := []struct {
		name   string
		source ScopeCatalogSnapshotSource
		clock  port.Clock
		ttl    time.Duration
	}{
		{name: "nil source", clock: validClock, ttl: time.Minute},
		{name: "nil clock", source: validSource, ttl: time.Minute},
		{name: "zero TTL", source: validSource, clock: validClock},
		{name: "negative TTL", source: validSource, clock: validClock, ttl: -time.Second},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cache, err := NewScopeCatalogCache(testCase.source, testCase.clock, testCase.ttl)
			if cache != nil || !errors.Is(err, ErrInvalidScopeCatalogCacheConfiguration) {
				t.Fatalf("NewScopeCatalogCache() = (%v, %v), want nil invalid configuration", cache, err)
			}
		})
	}
}

func TestNilScopeCatalogCacheFailsClosed(t *testing.T) {
	t.Parallel()

	var cache *ScopeCatalogCache
	revision, err := cache.EnsureAllRequestable(context.Background(), nil)
	if revision != 0 || !errors.Is(err, port.ErrScopeCatalogUnavailable) {
		t.Fatalf("nil cache = (%d, %v), want unavailable", revision, err)
	}
}
