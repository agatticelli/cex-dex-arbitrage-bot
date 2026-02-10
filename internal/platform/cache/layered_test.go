package cache

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// mockCache is a simple in-memory cache for testing
type mockCache struct {
	mu      sync.RWMutex
	data    map[string]mockEntry
	getErr  error // Error to return on Get
	setErr  error // Error to return on Set
	delErr  error // Error to return on Delete
	getCalls int
	setCalls int
}

type mockEntry struct {
	value   interface{}
	expires time.Time
}

func newMockCache() *mockCache {
	return &mockCache{
		data: make(map[string]mockEntry),
	}
}

func (m *mockCache) Get(ctx context.Context, key string) (interface{}, error) {
	m.mu.Lock()
	m.getCalls++
	m.mu.Unlock()

	if m.getErr != nil {
		return nil, m.getErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}

	// Check expiration
	if !entry.expires.IsZero() && time.Now().After(entry.expires) {
		return nil, ErrNotFound
	}

	return entry.value, nil
}

func (m *mockCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCalls++

	if m.setErr != nil {
		return m.setErr
	}

	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}

	m.data[key] = mockEntry{
		value:   value,
		expires: expires,
	}
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	if m.delErr != nil {
		return m.delErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *mockCache) Close() error {
	return nil
}

func (m *mockCache) getGetCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getCalls
}

func (m *mockCache) getSetCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.setCalls
}

// TestL1MissTriggersL2Lookup verifies that a miss in L1 triggers L2 lookup
func TestL1MissTriggersL2Lookup(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	// Set value only in L2
	testKey := "test-key"
	testValue := "test-value"
	if err := l2.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Failed to set L2 value: %v", err)
	}

	// Get should return L2 value despite L1 miss
	val, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Expected value from L2, got error: %v", err)
	}

	if val != testValue {
		t.Errorf("Expected value %q, got %q", testValue, val)
	}

	// Verify both caches were checked
	if l1.getGetCalls() != 1 {
		t.Errorf("Expected 1 L1 Get call, got %d", l1.getGetCalls())
	}
	if l2.getGetCalls() != 1 {
		t.Errorf("Expected 1 L2 Get call, got %d", l2.getGetCalls())
	}

	t.Log("✓ L1 miss correctly triggers L2 lookup")
}

// TestL2HitBackfillsL1 verifies that L2 hits are backfilled to L1
func TestL2HitBackfillsL1(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	// Set value only in L2
	testKey := "backfill-key"
	testValue := "backfill-value"
	if err := l2.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Failed to set L2 value: %v", err)
	}

	// First get - should trigger backfill
	val, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("First get failed: %v", err)
	}
	if val != testValue {
		t.Errorf("Expected value %q, got %q", testValue, val)
	}

	// Verify L1 was backfilled
	if l1.getSetCalls() != 1 {
		t.Errorf("Expected 1 L1 Set call (backfill), got %d", l1.getSetCalls())
	}

	// Second get - should hit L1 directly
	l1GetsBefore := l1.getGetCalls()
	l2GetsBefore := l2.getGetCalls()

	val2, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Second get failed: %v", err)
	}
	if val2 != testValue {
		t.Errorf("Expected value %q from L1, got %q", testValue, val2)
	}

	// Verify L1 was hit, L2 was not
	if l1.getGetCalls() != l1GetsBefore+1 {
		t.Errorf("Expected 1 additional L1 Get call, got %d", l1.getGetCalls()-l1GetsBefore)
	}
	if l2.getGetCalls() != l2GetsBefore {
		t.Errorf("Expected no additional L2 Get calls, got %d", l2.getGetCalls()-l2GetsBefore)
	}

	t.Log("✓ L2 hit correctly backfills L1")
}

// TestTTLRespectedPerLayer verifies that L1 gets shorter TTL than L2
func TestTTLRespectedPerLayer(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	// Create cache with 30s L1 max TTL
	lc := NewLayeredCacheWithConfig(LayeredCacheConfig{
		L1:       l1,
		L2:       l2,
		L1MaxTTL: 30 * time.Second,
	})

	testKey := "ttl-key"
	testValue := "ttl-value"
	longTTL := 5 * time.Minute // Longer than L1 max

	// Set with long TTL
	if err := lc.Set(ctx, testKey, testValue, longTTL); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify L1 got the capped TTL
	l1.mu.RLock()
	l1Entry, ok := l1.data[testKey]
	l1.mu.RUnlock()

	if !ok {
		t.Fatal("Key not found in L1")
	}

	// L1 TTL should be capped at 30s (allowing 1s margin for test execution)
	l1TTL := time.Until(l1Entry.expires)
	if l1TTL > 31*time.Second {
		t.Errorf("Expected L1 TTL <= 30s, got %v", l1TTL)
	}

	// Verify L2 got the full TTL
	l2.mu.RLock()
	l2Entry, ok := l2.data[testKey]
	l2.mu.RUnlock()

	if !ok {
		t.Fatal("Key not found in L2")
	}

	l2TTL := time.Until(l2Entry.expires)
	if l2TTL < 4*time.Minute { // Allow margin
		t.Errorf("Expected L2 TTL ~5 minutes, got %v", l2TTL)
	}

	t.Log("✓ TTL correctly capped for L1, full TTL for L2")
}

// TestGracefulDegradationOnL1Error verifies fallback to L2 when L1 fails
func TestGracefulDegradationOnL1Error(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	// Configure L1 to return errors
	l1.getErr = errors.New("L1 connection failed")

	// Create cache with logger to capture warnings
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	lc := NewLayeredCacheWithLogger(l1, l2, logger)

	// Set value only in L2
	testKey := "degradation-key"
	testValue := "degradation-value"
	l2.Set(ctx, testKey, testValue, time.Minute) // Direct set to L2

	// Get should fall back to L2 despite L1 error
	val, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Expected graceful degradation to L2, got error: %v", err)
	}

	if val != testValue {
		t.Errorf("Expected value %q from L2, got %q", testValue, val)
	}

	t.Log("✓ Graceful degradation to L2 on L1 error")
}

// TestL1OnlyMode verifies cache works with only L1
func TestL1OnlyMode(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()

	lc := NewLayeredCache(l1, nil)

	testKey := "l1-only-key"
	testValue := "l1-only-value"

	// Set
	if err := lc.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get
	val, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if val != testValue {
		t.Errorf("Expected value %q, got %q", testValue, val)
	}

	// Delete
	if err := lc.Delete(ctx, testKey); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	_, err = lc.Get(ctx, testKey)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected ErrNotFound after delete, got: %v", err)
	}

	t.Log("✓ L1-only mode works correctly")
}

// TestL2OnlyMode verifies cache works with only L2
func TestL2OnlyMode(t *testing.T) {
	ctx := context.Background()

	l2 := newMockCache()

	lc := NewLayeredCache(nil, l2)

	testKey := "l2-only-key"
	testValue := "l2-only-value"

	// Set
	if err := lc.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get
	val, err := lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if val != testValue {
		t.Errorf("Expected value %q, got %q", testValue, val)
	}

	t.Log("✓ L2-only mode works correctly")
}

// TestInvalidateL1 verifies that L1 can be invalidated independently
func TestInvalidateL1(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	testKey := "invalidate-key"
	testValue := "invalidate-value"

	// Set in both layers
	if err := lc.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Invalidate L1 only
	if err := lc.InvalidateL1(ctx, testKey); err != nil {
		t.Fatalf("InvalidateL1 failed: %v", err)
	}

	// Verify L1 is empty
	_, err := l1.Get(ctx, testKey)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected L1 to be invalidated, got: %v", err)
	}

	// Verify L2 still has value
	val, err := l2.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Expected L2 to still have value: %v", err)
	}
	if val != testValue {
		t.Errorf("Expected L2 value %q, got %q", testValue, val)
	}

	// Get via layered cache should re-populate L1 from L2
	val, err = lc.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("Get after invalidate failed: %v", err)
	}
	if val != testValue {
		t.Errorf("Expected value %q after re-populate, got %q", testValue, val)
	}

	t.Log("✓ InvalidateL1 correctly invalidates only L1")
}

// TestConcurrentAccess verifies thread safety
func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := "concurrent-key"
				lc.Set(ctx, key, id*1000+j, time.Minute)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				lc.Get(ctx, "concurrent-key")
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Error("Concurrent access test timed out - possible deadlock")
	}

	t.Log("✓ Concurrent access is thread-safe")
}

// TestNotFoundPropagation verifies ErrNotFound is returned when key doesn't exist
func TestNotFoundPropagation(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	_, err := lc.Get(ctx, "nonexistent-key")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected ErrNotFound, got: %v", err)
	}

	t.Log("✓ ErrNotFound correctly propagated for missing keys")
}

// TestL2ErrorPropagation verifies that L2 errors (non-ErrNotFound) are propagated
func TestL2ErrorPropagation(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	// L2 returns error
	l2.getErr = errors.New("L2 connection failed")

	lc := NewLayeredCache(l1, l2)

	_, err := lc.Get(ctx, "test-key")
	if err == nil {
		t.Error("Expected L2 error to be propagated")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("Expected actual error, not ErrNotFound")
	}

	t.Log("✓ L2 errors correctly propagated")
}

// TestSetWriteThrough verifies write-through to both layers
func TestSetWriteThrough(t *testing.T) {
	ctx := context.Background()

	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	testKey := "write-through-key"
	testValue := "write-through-value"

	if err := lc.Set(ctx, testKey, testValue, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify both layers have the value
	l1Val, err := l1.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("L1 should have value: %v", err)
	}
	if l1Val != testValue {
		t.Errorf("L1 value mismatch: expected %q, got %q", testValue, l1Val)
	}

	l2Val, err := l2.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("L2 should have value: %v", err)
	}
	if l2Val != testValue {
		t.Errorf("L2 value mismatch: expected %q, got %q", testValue, l2Val)
	}

	t.Log("✓ Write-through correctly stores in both layers")
}

// TestDefaultL1MaxTTL verifies the default L1 TTL is applied
func TestDefaultL1MaxTTL(t *testing.T) {
	l1 := newMockCache()
	l2 := newMockCache()

	lc := NewLayeredCache(l1, l2)

	// Verify default L1 max TTL
	if lc.l1MaxTTL != DefaultL1MaxTTL {
		t.Errorf("Expected default L1 max TTL %v, got %v", DefaultL1MaxTTL, lc.l1MaxTTL)
	}

	if DefaultL1MaxTTL != 1*time.Minute {
		t.Errorf("Expected DefaultL1MaxTTL to be 1 minute, got %v", DefaultL1MaxTTL)
	}

	t.Log("✓ Default L1 max TTL correctly set")
}
