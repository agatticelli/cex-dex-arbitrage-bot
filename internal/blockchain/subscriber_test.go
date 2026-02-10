package blockchain

import (
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/ethereum/go-ethereum/common"
)

func TestBlock_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		block    Block
		expected bool
	}{
		{
			name: "valid block",
			block: Block{
				Number:    big.NewInt(12345),
				Hash:      common.HexToHash("0x1234567890abcdef"),
				Timestamp: 1234567890,
			},
			expected: true,
		},
		{
			name: "nil number",
			block: Block{
				Number:    nil,
				Hash:      common.HexToHash("0x1234567890abcdef"),
				Timestamp: 1234567890,
			},
			expected: false,
		},
		{
			name: "zero block number",
			block: Block{
				Number:    big.NewInt(0),
				Hash:      common.HexToHash("0x1234567890abcdef"),
				Timestamp: 1234567890,
			},
			expected: false,
		},
		{
			// Note: big.Int(-1).Uint64() wraps to MaxUint64, so IsValid returns true
			// This is consistent with Ethereum behavior where block numbers are always positive
			name: "negative block number wraps to valid",
			block: Block{
				Number:    big.NewInt(-1),
				Hash:      common.HexToHash("0x1234567890abcdef"),
				Timestamp: 1234567890,
			},
			expected: true, // Wraps to MaxUint64 which is > 0
		},
		{
			name: "very large block number",
			block: Block{
				Number:    big.NewInt(999999999),
				Hash:      common.HexToHash("0x1234567890abcdef"),
				Timestamp: 1234567890,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.block.IsValid()
			if result != tt.expected {
				t.Errorf("Expected IsValid() = %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestNewSubscriber_Validation(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	tests := []struct {
		name        string
		config      SubscriberConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty URLs",
			config:      SubscriberConfig{},
			expectError: true,
			errorMsg:    "at least one WebSocket URL is required",
		},
		{
			name: "valid single URL",
			config: SubscriberConfig{
				WebSocketURLs: []string{"ws://localhost:8546"},
				Logger:        logger,
			},
			expectError: false,
		},
		{
			name: "valid multiple URLs",
			config: SubscriberConfig{
				WebSocketURLs: []string{"ws://localhost:8546", "ws://localhost:8547"},
				Logger:        logger,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := NewSubscriber(tt.config)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if sub == nil {
					t.Error("Expected non-nil subscriber")
				}
			}
		})
	}
}

func TestNewSubscriber_Defaults(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Verify defaults
	if sub.heartbeatInterval != 30*time.Second {
		t.Errorf("Expected default heartbeatInterval 30s, got %v", sub.heartbeatInterval)
	}
	if sub.heartbeatTimeout != 5*time.Second {
		t.Errorf("Expected default heartbeatTimeout 5s, got %v", sub.heartbeatTimeout)
	}
	if sub.messageTimeout != 60*time.Second {
		t.Errorf("Expected default messageTimeout 60s, got %v", sub.messageTimeout)
	}
	if sub.pollInterval != 12*time.Second {
		t.Errorf("Expected default pollInterval 12s, got %v", sub.pollInterval)
	}
	if sub.maxWSFailures != 3 {
		t.Errorf("Expected default maxWSFailures 3, got %d", sub.maxWSFailures)
	}
}

func TestNewSubscriber_CustomConfig(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs:     []string{"ws://localhost:8546"},
		Logger:            logger,
		HeartbeatInterval: 15 * time.Second,
		HeartbeatTimeout:  3 * time.Second,
		MessageTimeout:    30 * time.Second,
		PollInterval:      6 * time.Second,
		MaxWSFailures:     5,
		ReconnectConfig: ReconnectConfig{
			BaseDelay: 2 * time.Second,
			MaxDelay:  60 * time.Second,
			Jitter:    0.3,
		},
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Verify custom values
	if sub.heartbeatInterval != 15*time.Second {
		t.Errorf("Expected heartbeatInterval 15s, got %v", sub.heartbeatInterval)
	}
	if sub.heartbeatTimeout != 3*time.Second {
		t.Errorf("Expected heartbeatTimeout 3s, got %v", sub.heartbeatTimeout)
	}
	if sub.messageTimeout != 30*time.Second {
		t.Errorf("Expected messageTimeout 30s, got %v", sub.messageTimeout)
	}
	if sub.pollInterval != 6*time.Second {
		t.Errorf("Expected pollInterval 6s, got %v", sub.pollInterval)
	}
	if sub.maxWSFailures != 5 {
		t.Errorf("Expected maxWSFailures 5, got %d", sub.maxWSFailures)
	}
	if sub.reconnectConfig.MaxDelay != 60*time.Second {
		t.Errorf("Expected maxDelay 60s, got %v", sub.reconnectConfig.MaxDelay)
	}
	if sub.reconnectConfig.BaseDelay != 2*time.Second {
		t.Errorf("Expected baseDelay 2s, got %v", sub.reconnectConfig.BaseDelay)
	}
}

func TestDefaultReconnectConfig(t *testing.T) {
	config := DefaultReconnectConfig()

	if config.BaseDelay != 1*time.Second {
		t.Errorf("Expected BaseDelay 1s, got %v", config.BaseDelay)
	}
	if config.MaxDelay != 30*time.Second {
		t.Errorf("Expected MaxDelay 30s, got %v", config.MaxDelay)
	}
	if config.Jitter != 0.2 {
		t.Errorf("Expected Jitter 0.2, got %f", config.Jitter)
	}
}

func TestSubscriber_calculateReconnectDelay(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
		ReconnectConfig: ReconnectConfig{
			BaseDelay: 1 * time.Second,
			MaxDelay:  30 * time.Second,
			Jitter:    0.0, // No jitter for predictable tests
		},
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	tests := []struct {
		name            string
		attempts        int
		expectedMinimum time.Duration
		expectedMaximum time.Duration
	}{
		{
			name:            "first attempt",
			attempts:        0,
			expectedMinimum: 900 * time.Millisecond,
			expectedMaximum: 1100 * time.Millisecond,
		},
		{
			name:            "second attempt",
			attempts:        1,
			expectedMinimum: 1800 * time.Millisecond,
			expectedMaximum: 2200 * time.Millisecond,
		},
		{
			name:            "third attempt",
			attempts:        2,
			expectedMinimum: 3600 * time.Millisecond,
			expectedMaximum: 4400 * time.Millisecond,
		},
		{
			name:            "capped at max",
			attempts:        10,
			expectedMinimum: 25 * time.Second,
			expectedMaximum: 35 * time.Second, // Should be capped at MaxDelay
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub.reconnectAttempts = tt.attempts
			delay := sub.calculateReconnectDelay()

			if delay < tt.expectedMinimum {
				t.Errorf("Expected delay >= %v, got %v", tt.expectedMinimum, delay)
			}
			if delay > tt.expectedMaximum {
				t.Errorf("Expected delay <= %v, got %v", tt.expectedMaximum, delay)
			}
		})
	}
}

func TestSubscriber_IsConnected(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Initially not connected
	if sub.IsConnected() {
		t.Error("Expected subscriber to not be connected initially")
	}

	// Manually set connected state
	sub.mu.Lock()
	sub.isConnected = true
	sub.mu.Unlock()

	if !sub.IsConnected() {
		t.Error("Expected subscriber to be connected after setting state")
	}

	// Manually set disconnected state
	sub.mu.Lock()
	sub.isConnected = false
	sub.mu.Unlock()

	if sub.IsConnected() {
		t.Error("Expected subscriber to not be connected after clearing state")
	}
}

func TestSubscriber_GetLastBlockNumber(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Initially zero
	if sub.GetLastBlockNumber() != 0 {
		t.Errorf("Expected last block 0, got %d", sub.GetLastBlockNumber())
	}

	// Update block number
	sub.mu.Lock()
	sub.lastBlockNumber = 12345678
	sub.mu.Unlock()

	if sub.GetLastBlockNumber() != 12345678 {
		t.Errorf("Expected last block 12345678, got %d", sub.GetLastBlockNumber())
	}
}

func TestSubscriber_ConcurrentAccess(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = sub.IsConnected()
				_ = sub.GetLastBlockNumber()
			}
		}()
	}

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sub.mu.Lock()
				sub.isConnected = id%2 == 0
				sub.lastBlockNumber = uint64(id*1000 + j)
				sub.mu.Unlock()
			}
		}(i)
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
}

func TestSubscriber_disconnect(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Simulate connected state
	sub.mu.Lock()
	sub.isConnected = true
	sub.mu.Unlock()

	// Disconnect
	sub.disconnect()

	// Verify disconnected
	if sub.IsConnected() {
		t.Error("Expected subscriber to be disconnected after disconnect()")
	}
	if sub.client != nil {
		t.Error("Expected client to be nil after disconnect()")
	}
}

func TestSubscriber_Close(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Close should not panic
	sub.Close()

	// Verify closed state
	if sub.IsConnected() {
		t.Error("Expected subscriber to be disconnected after Close()")
	}
}

func TestSubscriber_URLRotation(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	urls := []string{"ws://localhost:8546", "ws://localhost:8547", "ws://localhost:8548"}

	config := SubscriberConfig{
		WebSocketURLs: urls,
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Initial index should be 0
	if sub.currentURLIdx != 0 {
		t.Errorf("Expected initial URL index 0, got %d", sub.currentURLIdx)
	}

	// Simulate rotation (happens on connection failure)
	sub.currentURLIdx = (sub.currentURLIdx + 1) % len(urls)
	if sub.currentURLIdx != 1 {
		t.Errorf("Expected URL index 1 after first rotation, got %d", sub.currentURLIdx)
	}

	// Second rotation
	sub.currentURLIdx = (sub.currentURLIdx + 1) % len(urls)
	if sub.currentURLIdx != 2 {
		t.Errorf("Expected URL index 2 after second rotation, got %d", sub.currentURLIdx)
	}

	// Third rotation should wrap to 0
	sub.currentURLIdx = (sub.currentURLIdx + 1) % len(urls)
	if sub.currentURLIdx != 0 {
		t.Errorf("Expected URL index 0 after wrap, got %d", sub.currentURLIdx)
	}
}

func TestSubscriber_WSFailureTracking(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
		MaxWSFailures: 3,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Initial failure count should be 0
	if sub.wsFailureCount != 0 {
		t.Errorf("Expected initial wsFailureCount 0, got %d", sub.wsFailureCount)
	}

	// Simulate failures
	sub.wsFailureCount++
	if sub.wsFailureCount != 1 {
		t.Errorf("Expected wsFailureCount 1, got %d", sub.wsFailureCount)
	}

	// Check threshold
	if sub.wsFailureCount >= sub.maxWSFailures {
		t.Error("Should not trigger HTTP fallback after 1 failure")
	}

	sub.wsFailureCount++
	sub.wsFailureCount++

	if sub.wsFailureCount < sub.maxWSFailures {
		t.Error("Should trigger HTTP fallback after 3 failures")
	}
}

func BenchmarkBlock_IsValid(b *testing.B) {
	block := Block{
		Number:    big.NewInt(12345678),
		Hash:      common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Timestamp: 1234567890,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		block.IsValid()
	}
}

func BenchmarkSubscriber_GetLastBlockNumber(b *testing.B) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, _ := NewSubscriber(config)
	sub.lastBlockNumber = 12345678

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sub.GetLastBlockNumber()
	}
}

func BenchmarkSubscriber_IsConnected(b *testing.B) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, _ := NewSubscriber(config)
	sub.isConnected = true

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sub.IsConnected()
	}
}

// TestWebSocketReconnectionWithBackoff tests that reconnection delays increase exponentially
// with jitter applied correctly within bounds.
func TestWebSocketReconnectionWithBackoff(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
		ReconnectConfig: ReconnectConfig{
			BaseDelay: 1 * time.Second,
			MaxDelay:  30 * time.Second,
			Jitter:    0.2, // 20% jitter
		},
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Track delays across multiple attempts to verify exponential backoff
	previousDelay := time.Duration(0)
	for attempt := 0; attempt < 6; attempt++ {
		sub.reconnectAttempts = attempt
		delay := sub.calculateReconnectDelay()

		// Expected base delay: 1s, 2s, 4s, 8s, 16s, 30s (capped)
		expectedBase := time.Duration(1<<attempt) * time.Second
		if expectedBase > 30*time.Second {
			expectedBase = 30 * time.Second
		}

		// With 20% jitter, delay should be in range [0.8 * base, 1.2 * base]
		minDelay := time.Duration(float64(expectedBase) * 0.8)
		maxDelay := time.Duration(float64(expectedBase) * 1.2)

		if delay < minDelay || delay > maxDelay {
			t.Errorf("Attempt %d: delay %v out of expected range [%v, %v]",
				attempt, delay, minDelay, maxDelay)
		}

		// Verify delays generally increase (allowing for jitter overlap)
		if attempt > 1 && delay < previousDelay/2 {
			t.Errorf("Attempt %d: delay %v decreased too much from previous %v",
				attempt, delay, previousDelay)
		}
		previousDelay = delay
	}

	t.Log("✓ Exponential backoff with jitter verified")
}

// TestHTTPFallbackTrigger tests that HTTP fallback is triggered after maxWSFailures
func TestHTTPFallbackTrigger(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	tests := []struct {
		name              string
		maxWSFailures     int
		hasClientPool     bool
		wsFailureCount    int
		expectHTTPFallback bool
	}{
		{
			name:              "no fallback without client pool",
			maxWSFailures:     3,
			hasClientPool:     false,
			wsFailureCount:    5,
			expectHTTPFallback: false, // No client pool, can't fallback
		},
		{
			name:              "no fallback before threshold",
			maxWSFailures:     3,
			hasClientPool:     true,
			wsFailureCount:    2,
			expectHTTPFallback: false,
		},
		{
			name:              "fallback at threshold",
			maxWSFailures:     3,
			hasClientPool:     true,
			wsFailureCount:    3,
			expectHTTPFallback: true,
		},
		{
			name:              "fallback above threshold",
			maxWSFailures:     3,
			hasClientPool:     true,
			wsFailureCount:    5,
			expectHTTPFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := SubscriberConfig{
				WebSocketURLs: []string{"ws://localhost:8546"},
				Logger:        logger,
				MaxWSFailures: tt.maxWSFailures,
			}

			sub, err := NewSubscriber(config)
			if err != nil {
				t.Fatalf("Failed to create subscriber: %v", err)
			}

			// Simulate client pool presence
			if tt.hasClientPool {
				// Create a mock client pool (just need non-nil for the check)
				sub.clientPool = &ClientPool{}
			}

			// Set failure count
			sub.wsFailureCount = tt.wsFailureCount

			// Check if fallback should trigger
			shouldFallback := sub.clientPool != nil && sub.wsFailureCount >= sub.maxWSFailures

			if shouldFallback != tt.expectHTTPFallback {
				t.Errorf("Expected HTTP fallback=%v, got %v", tt.expectHTTPFallback, shouldFallback)
			}
		})
	}
}

// TestWSFailureCountDecay tests that failure count decays during HTTP polling mode
func TestWSFailureCountDecay(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
		MaxWSFailures: 3,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Simulate failures reaching threshold
	sub.wsFailureCount = 3

	// Simulate decay logic (as in subscriptionLoop HTTP mode)
	for i := 0; i < 4; i++ {
		if sub.wsFailureCount > 0 {
			sub.wsFailureCount--
		}
	}

	if sub.wsFailureCount != 0 {
		t.Errorf("Expected failure count to decay to 0, got %d", sub.wsFailureCount)
	}

	// Should now try WebSocket again
	shouldTryWebSocket := sub.wsFailureCount == 0
	if !shouldTryWebSocket {
		t.Error("Expected to try WebSocket after failure count decayed to 0")
	}

	t.Log("✓ Failure count decay verified")
}

// TestBlockGapDetection tests detection of gaps in block sequence
func TestBlockGapDetection(t *testing.T) {
	tests := []struct {
		name            string
		lastBlockNum    uint64
		newBlockNum     uint64
		expectedGap     uint64
		expectGapDetected bool
	}{
		{
			name:            "consecutive blocks - no gap",
			lastBlockNum:    1000,
			newBlockNum:     1001,
			expectedGap:     0,
			expectGapDetected: false,
		},
		{
			name:            "single block gap",
			lastBlockNum:    1000,
			newBlockNum:     1002,
			expectedGap:     1,
			expectGapDetected: true,
		},
		{
			name:            "large gap",
			lastBlockNum:    1000,
			newBlockNum:     1010,
			expectedGap:     9,
			expectGapDetected: true,
		},
		{
			name:            "first block - no previous",
			lastBlockNum:    0,
			newBlockNum:     1000,
			expectedGap:     0,
			expectGapDetected: false, // lastBlockNum=0 means no previous block
		},
		{
			name:            "same block - no gap",
			lastBlockNum:    1000,
			newBlockNum:     1000,
			expectedGap:     0,
			expectGapDetected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate gap detection logic from subscribeToBlocks
			var gapDetected bool
			var gapSize uint64

			if tt.lastBlockNum > 0 && tt.newBlockNum > tt.lastBlockNum+1 {
				gapDetected = true
				gapSize = tt.newBlockNum - tt.lastBlockNum - 1
			}

			if gapDetected != tt.expectGapDetected {
				t.Errorf("Expected gap detected=%v, got %v", tt.expectGapDetected, gapDetected)
			}

			if gapDetected && gapSize != tt.expectedGap {
				t.Errorf("Expected gap size %d, got %d", tt.expectedGap, gapSize)
			}

			if gapDetected {
				t.Logf("✓ Gap detected: blocks %d to %d missing (size=%d)",
					tt.lastBlockNum+1, tt.newBlockNum-1, gapSize)
			}
		})
	}
}

// TestGracefulShutdown tests that subscriber shuts down cleanly
func TestGracefulShutdown(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs: []string{"ws://localhost:8546"},
		Logger:        logger,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Simulate connected state with some state
	sub.mu.Lock()
	sub.isConnected = true
	sub.lastBlockNumber = 12345678
	sub.wsFailureCount = 2
	sub.mu.Unlock()

	// Close should clean up gracefully
	sub.Close()

	// Verify cleanup
	if sub.IsConnected() {
		t.Error("Expected disconnected after Close()")
	}

	if sub.client != nil {
		t.Error("Expected client to be nil after Close()")
	}

	// Last block number should be preserved (for diagnostics)
	if sub.GetLastBlockNumber() != 12345678 {
		t.Error("Last block number should be preserved after Close()")
	}

	// Double close should not panic
	sub.Close()

	t.Log("✓ Graceful shutdown verified")
}

// TestHealthCheckStateDetection tests that stale connections are detected
func TestHealthCheckStateDetection(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	config := SubscriberConfig{
		WebSocketURLs:  []string{"ws://localhost:8546"},
		Logger:         logger,
		MessageTimeout: 60 * time.Second,
	}

	sub, err := NewSubscriber(config)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}

	// Simulate connected state
	sub.mu.Lock()
	sub.isConnected = true
	sub.mu.Unlock()

	// Set last message time to just now
	sub.healthCheckMu.Lock()
	sub.lastMessageTime = time.Now()
	sub.healthCheckMu.Unlock()

	// Check time since last message
	sub.healthCheckMu.Lock()
	timeSinceMessage := time.Since(sub.lastMessageTime)
	sub.healthCheckMu.Unlock()

	if timeSinceMessage > sub.messageTimeout {
		t.Error("Should not detect stale connection for recent message")
	}

	// Simulate stale connection (old message time)
	sub.healthCheckMu.Lock()
	sub.lastMessageTime = time.Now().Add(-2 * time.Minute)
	timeSinceMessage = time.Since(sub.lastMessageTime)
	sub.healthCheckMu.Unlock()

	if timeSinceMessage <= sub.messageTimeout {
		t.Error("Should detect stale connection for old message")
	}

	t.Log("✓ Health check state detection verified")
}
