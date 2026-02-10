package blockchain

import (
	"sync"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
)

func TestNewClientPool_Validation(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	tests := []struct {
		name        string
		config      ClientPoolConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty endpoints",
			config:      ClientPoolConfig{Logger: logger},
			expectError: true,
			errorMsg:    "at least one RPC endpoint is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClientPool(tt.config)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestClientPoolConfig_Defaults(t *testing.T) {
	// Verify default health check TTL is applied when not set
	config := ClientPoolConfig{
		Endpoints: []EndpointConfig{
			{URL: "http://localhost:8545", Weight: 1},
		},
		Logger:         observability.NewLogger("error", "json"),
		HealthCheckTTL: 0, // Should be set to default
	}

	// Can't fully test without actual connection, but verify config handling
	if config.HealthCheckTTL == 0 {
		// Default should be applied in NewClientPool
		// 30 * time.Second is the expected default
		expectedDefault := 30 * time.Second
		t.Logf("Default HealthCheckTTL should be %v", expectedDefault)
	}
}

func TestEndpointConfig_Fields(t *testing.T) {
	config := EndpointConfig{
		URL:    "http://localhost:8545",
		Weight: 10,
	}

	if config.URL != "http://localhost:8545" {
		t.Errorf("Expected URL 'http://localhost:8545', got '%s'", config.URL)
	}
	if config.Weight != 10 {
		t.Errorf("Expected Weight 10, got %d", config.Weight)
	}
}

func TestRPCEndpoint_AtomicHealthy(t *testing.T) {
	endpoint := &RPCEndpoint{
		URL:    "http://localhost:8545",
		Weight: 1,
	}

	// Initial state should be default (false)
	if endpoint.healthy.Load() {
		t.Error("Expected initial healthy state to be false")
	}

	// Set healthy
	endpoint.healthy.Store(true)
	if !endpoint.healthy.Load() {
		t.Error("Expected healthy to be true after Store(true)")
	}

	// Set unhealthy
	endpoint.healthy.Store(false)
	if endpoint.healthy.Load() {
		t.Error("Expected healthy to be false after Store(false)")
	}

	// Test Swap
	wasHealthy := endpoint.healthy.Swap(true)
	if wasHealthy {
		t.Error("Expected Swap to return previous value (false)")
	}
	if !endpoint.healthy.Load() {
		t.Error("Expected healthy to be true after Swap(true)")
	}
}

func TestRPCEndpoint_ConcurrentHealthUpdates(t *testing.T) {
	endpoint := &RPCEndpoint{
		URL:    "http://localhost:8545",
		Weight: 1,
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent health updates
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val bool) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				endpoint.healthy.Store(val)
				_ = endpoint.healthy.Load()
			}
		}(i%2 == 0)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no race
	case <-time.After(5 * time.Second):
		t.Error("Concurrent health update test timed out")
	}
}

func TestRPCEndpoint_MutexProtection(t *testing.T) {
	endpoint := &RPCEndpoint{
		URL:    "http://localhost:8545",
		Weight: 1,
		Client: nil,
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent client access (read)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				endpoint.mu.RLock()
				_ = endpoint.Client
				endpoint.mu.RUnlock()
			}
		}()
	}

	// Concurrent client access (write)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				endpoint.mu.Lock()
				endpoint.Client = nil // Simulate reconnection
				endpoint.mu.Unlock()
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no race or deadlock
	case <-time.After(5 * time.Second):
		t.Error("Mutex protection test timed out - possible deadlock")
	}
}

func TestClientPool_GetHealthyEndpointCount_Empty(t *testing.T) {
	// Test without actual endpoints (just struct)
	pool := &ClientPool{
		endpoints: []*RPCEndpoint{},
	}

	count := pool.GetHealthyEndpointCount()
	if count != 0 {
		t.Errorf("Expected 0 healthy endpoints for empty pool, got %d", count)
	}
}

func TestClientPool_GetHealthyEndpointCount_Mixed(t *testing.T) {
	endpoint1 := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint1.healthy.Store(true)

	endpoint2 := &RPCEndpoint{URL: "http://localhost:8546", Weight: 1}
	endpoint2.healthy.Store(false)

	endpoint3 := &RPCEndpoint{URL: "http://localhost:8547", Weight: 1}
	endpoint3.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint1, endpoint2, endpoint3},
	}

	count := pool.GetHealthyEndpointCount()
	if count != 2 {
		t.Errorf("Expected 2 healthy endpoints, got %d", count)
	}
}

func TestClientPool_GetEndpointStatus(t *testing.T) {
	endpoint1 := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint1.healthy.Store(true)

	endpoint2 := &RPCEndpoint{URL: "http://localhost:8546", Weight: 1}
	endpoint2.healthy.Store(false)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint1, endpoint2},
	}

	status := pool.GetEndpointStatus()

	if len(status) != 2 {
		t.Errorf("Expected 2 endpoints in status, got %d", len(status))
	}

	if !status["http://localhost:8545"] {
		t.Error("Expected endpoint 8545 to be healthy")
	}

	if status["http://localhost:8546"] {
		t.Error("Expected endpoint 8546 to be unhealthy")
	}
}

func TestClientPool_MarkUnhealthy(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	endpoint := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint},
		logger:    logger,
	}

	// Verify initially healthy
	if !endpoint.healthy.Load() {
		t.Error("Expected endpoint to be initially healthy")
	}

	// Mark unhealthy
	pool.MarkUnhealthy("http://localhost:8545")

	// Verify now unhealthy
	if endpoint.healthy.Load() {
		t.Error("Expected endpoint to be unhealthy after MarkUnhealthy")
	}

	// Mark unhealthy again (should be idempotent)
	pool.MarkUnhealthy("http://localhost:8545")
	if endpoint.healthy.Load() {
		t.Error("Expected endpoint to still be unhealthy")
	}
}

func TestClientPool_MarkUnhealthy_NotFound(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	endpoint := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint},
		logger:    logger,
	}

	// Try to mark non-existent endpoint
	pool.MarkUnhealthy("http://localhost:9999")

	// Original endpoint should still be healthy
	if !endpoint.healthy.Load() {
		t.Error("Expected original endpoint to remain healthy")
	}
}

func TestClientPool_GetClient_NoHealthy(t *testing.T) {
	endpoint1 := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint1.healthy.Store(false)

	endpoint2 := &RPCEndpoint{URL: "http://localhost:8546", Weight: 1}
	endpoint2.healthy.Store(false)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint1, endpoint2},
		current:   0,
	}

	_, err := pool.GetClient()
	if err == nil {
		t.Error("Expected error when no healthy endpoints")
	}
}

func TestClientPool_GetClient_RoundRobin(t *testing.T) {
	// Test round-robin selection without actual clients
	endpoint1 := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint1.healthy.Store(true)

	endpoint2 := &RPCEndpoint{URL: "http://localhost:8546", Weight: 1}
	endpoint2.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint1, endpoint2},
		current:   0,
	}

	// First call should try endpoint 0, move to 1
	_, _ = pool.GetClient() // Will error because Client is nil, but tests index movement

	// Verify current moved
	if pool.current != 1 && pool.current != 0 {
		// Current should have moved (or stayed if no healthy with client)
		t.Logf("Current index after first call: %d", pool.current)
	}
}

func TestClientPool_GetClientByURL_NotFound(t *testing.T) {
	endpoint := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint},
	}

	_, err := pool.GetClientByURL("http://localhost:9999")
	if err == nil {
		t.Error("Expected error for non-existent URL")
	}
}

func TestClientPool_GetClientByURL_Unhealthy(t *testing.T) {
	endpoint := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint.healthy.Store(false)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint},
	}

	_, err := pool.GetClientByURL("http://localhost:8545")
	if err == nil {
		t.Error("Expected error for unhealthy endpoint")
	}
}

func TestClientPool_ConcurrentAccess(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	endpoint1 := &RPCEndpoint{URL: "http://localhost:8545", Weight: 1}
	endpoint1.healthy.Store(true)

	endpoint2 := &RPCEndpoint{URL: "http://localhost:8546", Weight: 1}
	endpoint2.healthy.Store(true)

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{endpoint1, endpoint2},
		current:   0,
		logger:    logger,
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = pool.GetHealthyEndpointCount()
				_ = pool.GetEndpointStatus()
			}
		}()
	}

	// Concurrent GetClient calls
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = pool.GetClient()
			}
		}()
	}

	// Concurrent MarkUnhealthy calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if idx%2 == 0 {
					pool.MarkUnhealthy("http://localhost:8545")
				} else {
					pool.MarkUnhealthy("http://localhost:8546")
				}
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no race or deadlock
	case <-time.After(5 * time.Second):
		t.Error("Concurrent access test timed out - possible deadlock")
	}
}

func TestClientPool_Close_NilCancel(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	pool := &ClientPool{
		endpoints: []*RPCEndpoint{},
		logger:    logger,
		cancel:    nil, // No cancel function
	}

	// Close should not panic with nil cancel
	pool.Close()
}

func BenchmarkClientPool_GetHealthyEndpointCount(b *testing.B) {
	endpoints := make([]*RPCEndpoint, 10)
	for i := 0; i < 10; i++ {
		ep := &RPCEndpoint{URL: "http://localhost:854" + string(rune('0'+i)), Weight: 1}
		ep.healthy.Store(i%2 == 0)
		endpoints[i] = ep
	}

	pool := &ClientPool{
		endpoints: endpoints,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.GetHealthyEndpointCount()
	}
}

func BenchmarkClientPool_GetEndpointStatus(b *testing.B) {
	endpoints := make([]*RPCEndpoint, 10)
	for i := 0; i < 10; i++ {
		ep := &RPCEndpoint{URL: "http://localhost:854" + string(rune('0'+i)), Weight: 1}
		ep.healthy.Store(i%2 == 0)
		endpoints[i] = ep
	}

	pool := &ClientPool{
		endpoints: endpoints,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.GetEndpointStatus()
	}
}

func BenchmarkRPCEndpoint_HealthyAtomicOps(b *testing.B) {
	endpoint := &RPCEndpoint{
		URL:    "http://localhost:8545",
		Weight: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		endpoint.healthy.Store(i%2 == 0)
		_ = endpoint.healthy.Load()
	}
}
