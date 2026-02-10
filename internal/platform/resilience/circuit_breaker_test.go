package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestStateTransitions_ClosedToOpen verifies circuit opens after failure threshold
func TestStateTransitions_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-closed-to-open",
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          1 * time.Second,
	})

	// Verify initial state is closed
	if cb.State() != StateClosed {
		t.Fatalf("Expected initial state Closed, got %s", cb.State())
	}

	// Simulate failures
	failErr := errors.New("test failure")
	for i := 0; i < 2; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return failErr
		})

		// Should still be closed
		if cb.State() != StateClosed {
			t.Errorf("Expected Closed after %d failures, got %s", i+1, cb.State())
		}
	}

	// Third failure should trigger open
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return failErr
	})

	if cb.State() != StateOpen {
		t.Errorf("Expected Open after 3 failures, got %s", cb.State())
	}

	// Verify requests are rejected when open
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}

	t.Log("✓ State transition Closed → Open works correctly")
}

// TestStateTransitions_OpenToHalfOpen verifies circuit transitions to half-open after timeout
func TestStateTransitions_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-open-to-half-open",
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          100 * time.Millisecond, // Short timeout for testing
	})

	// Force open state
	cb.ForceOpen()

	if cb.State() != StateOpen {
		t.Fatalf("Expected Open after ForceOpen, got %s", cb.State())
	}

	// Requests should be rejected immediately
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen before timeout, got %v", err)
	}

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Next request should transition to half-open and allow execution
	executed := false
	err = cb.Execute(context.Background(), func(ctx context.Context) error {
		executed = true
		return nil
	})

	if err != nil {
		t.Errorf("Expected request to succeed after timeout, got %v", err)
	}
	if !executed {
		t.Error("Expected function to be executed")
	}

	// Should now be closed (1 success threshold met)
	if cb.State() != StateClosed {
		t.Errorf("Expected Closed after successful half-open request, got %s", cb.State())
	}

	t.Log("✓ State transition Open → HalfOpen → Closed works correctly")
}

// TestStateTransitions_HalfOpenToClosed verifies circuit closes after success threshold in half-open
func TestStateTransitions_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-half-open-to-closed",
		FailureThreshold: 1,
		SuccessThreshold: 3, // Need 3 successes to close
		Timeout:          100 * time.Millisecond,
	})

	// Force open and wait for timeout
	cb.ForceOpen()
	time.Sleep(150 * time.Millisecond)

	// First request transitions to half-open
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil // Success
	})

	// Should still be half-open (need 3 successes, only have 1)
	// Note: The state after first success should be half-open since we need 3 successes
	// Let's verify by checking if more requests are allowed
	for i := 0; i < 2; i++ {
		err := cb.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Errorf("Request %d failed unexpectedly: %v", i+2, err)
		}
	}

	// After 3 successes, should be closed
	if cb.State() != StateClosed {
		t.Errorf("Expected Closed after 3 successes in half-open, got %s", cb.State())
	}

	t.Log("✓ State transition HalfOpen → Closed works correctly")
}

// TestHalfOpenToOpenOnFailure verifies circuit returns to open on failure in half-open
func TestHalfOpenToOpenOnFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-half-open-to-open",
		FailureThreshold: 1,
		SuccessThreshold: 3,
		Timeout:          100 * time.Millisecond,
	})

	// Force open and wait for timeout
	cb.ForceOpen()
	time.Sleep(150 * time.Millisecond)

	// First request transitions to half-open, but fails
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failed in half-open")
	})

	// Should be back to open
	if cb.State() != StateOpen {
		t.Errorf("Expected Open after failure in half-open, got %s", cb.State())
	}

	// Subsequent requests should be rejected
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen after half-open failure, got %v", err)
	}

	t.Log("✓ State transition HalfOpen → Open on failure works correctly")
}

// TestIgnoresContextCancellation verifies context.Canceled doesn't affect state
func TestIgnoresContextCancellation(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-ignore-context",
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          1 * time.Second,
	})

	// Context cancellations should not count as failures
	for i := 0; i < 5; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return context.Canceled
		})
	}

	// Circuit should still be closed
	if cb.State() != StateClosed {
		t.Errorf("Expected Closed after context cancellations, got %s", cb.State())
	}

	// Same for deadline exceeded
	for i := 0; i < 5; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return context.DeadlineExceeded
		})
	}

	// Circuit should still be closed
	if cb.State() != StateClosed {
		t.Errorf("Expected Closed after deadline exceeded errors, got %s", cb.State())
	}

	t.Log("✓ Context cancellation correctly ignored for state transitions")
}

// TestOnStateChangeCallback verifies callback is invoked on state transitions
func TestOnStateChangeCallback(t *testing.T) {
	var transitions []struct {
		from, to State
	}
	var mu sync.Mutex

	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-callback",
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          100 * time.Millisecond,
		OnStateChange: func(from, to State) {
			mu.Lock()
			transitions = append(transitions, struct{ from, to State }{from, to})
			mu.Unlock()
		},
	})

	// Trigger Closed → Open
	for i := 0; i < 2; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	// Wait for timeout and trigger Open → HalfOpen → Closed
	time.Sleep(150 * time.Millisecond)
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	mu.Lock()
	defer mu.Unlock()

	// Verify transitions
	expectedTransitions := []struct {
		from, to State
	}{
		{StateClosed, StateOpen},
		{StateOpen, StateHalfOpen},
		{StateHalfOpen, StateClosed},
	}

	if len(transitions) != len(expectedTransitions) {
		t.Errorf("Expected %d transitions, got %d", len(expectedTransitions), len(transitions))
		for i, tr := range transitions {
			t.Logf("  Transition %d: %s → %s", i, tr.from, tr.to)
		}
		return
	}

	for i, expected := range expectedTransitions {
		if transitions[i].from != expected.from || transitions[i].to != expected.to {
			t.Errorf("Transition %d: expected %s → %s, got %s → %s",
				i, expected.from, expected.to, transitions[i].from, transitions[i].to)
		}
	}

	t.Log("✓ OnStateChange callback invoked correctly for all transitions")
}

// TestSuccessResetsFailureCount verifies successful requests reset failure counter
func TestSuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-success-reset",
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout:          1 * time.Second,
	})

	// 2 failures
	for i := 0; i < 2; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	// 1 success should reset failure count
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	// 2 more failures should NOT trigger open (because counter was reset)
	for i := 0; i < 2; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	if cb.State() != StateClosed {
		t.Errorf("Expected Closed (failures reset by success), got %s", cb.State())
	}

	// Third failure should trigger open
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	if cb.State() != StateOpen {
		t.Errorf("Expected Open after 3 consecutive failures, got %s", cb.State())
	}

	t.Log("✓ Success correctly resets failure count")
}

// TestDefaultConfiguration verifies default values are applied
func TestDefaultConfiguration(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name: "test-defaults",
	})

	// Verify defaults
	if cb.failureThreshold != 5 {
		t.Errorf("Expected default failureThreshold 5, got %d", cb.failureThreshold)
	}
	if cb.successThreshold != 2 {
		t.Errorf("Expected default successThreshold 2, got %d", cb.successThreshold)
	}
	if cb.timeout != 60*time.Second {
		t.Errorf("Expected default timeout 60s, got %v", cb.timeout)
	}
	if cb.State() != StateClosed {
		t.Errorf("Expected initial state Closed, got %s", cb.State())
	}

	t.Log("✓ Default configuration applied correctly")
}

// TestReset verifies manual reset works
func TestReset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-reset",
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          1 * time.Hour, // Long timeout
	})

	// Trigger open
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	if cb.State() != StateOpen {
		t.Fatalf("Expected Open, got %s", cb.State())
	}

	// Manual reset
	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("Expected Closed after reset, got %s", cb.State())
	}

	// Should allow requests again
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("Expected request to succeed after reset, got %v", err)
	}

	t.Log("✓ Manual reset works correctly")
}

// TestForceOpen verifies force open works
func TestForceOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-force-open",
		FailureThreshold: 5,
		SuccessThreshold: 1,
		Timeout:          1 * time.Hour,
	})

	// Force open without failures
	cb.ForceOpen()

	if cb.State() != StateOpen {
		t.Errorf("Expected Open after ForceOpen, got %s", cb.State())
	}

	// Requests should be rejected
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}

	t.Log("✓ ForceOpen works correctly")
}

// TestStats verifies stats are tracked correctly
func TestStats(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-stats",
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          1 * time.Second,
	})

	// Initial stats
	state, failures, successes := cb.Stats()
	if state != StateClosed || failures != 0 || successes != 0 {
		t.Errorf("Initial stats wrong: state=%s, failures=%d, successes=%d", state, failures, successes)
	}

	// Record failures
	for i := 0; i < 2; i++ {
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	state, failures, successes = cb.Stats()
	if failures != 2 {
		t.Errorf("Expected 2 failures, got %d", failures)
	}

	// Record success
	_ = cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	_, failures, successes = cb.Stats()
	if failures != 0 { // Reset by success
		t.Errorf("Expected 0 failures after success, got %d", failures)
	}
	if successes != 1 {
		t.Errorf("Expected 1 success, got %d", successes)
	}

	t.Log("✓ Stats tracked correctly")
}

// TestConcurrentAccess verifies thread safety
func TestCircuitBreakerConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-concurrent",
		FailureThreshold: 100,
		SuccessThreshold: 10,
		Timeout:          1 * time.Second,
	})

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent executions
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = cb.Execute(context.Background(), func(ctx context.Context) error {
					if id%3 == 0 {
						return errors.New("failure")
					}
					return nil
				})
				_ = cb.State()
				_, _, _ = cb.Stats()
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

	t.Log("✓ Concurrent access is thread-safe")
}

// TestExecuteWithResult verifies generic execution with result
func TestExecuteWithResult(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "test-with-result",
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          1 * time.Second,
	})

	// Successful execution with result
	result, err := ExecuteWithResult(cb, context.Background(), func(ctx context.Context) (string, error) {
		return "success", nil
	})

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}
	if result != "success" {
		t.Errorf("Expected 'success', got %q", result)
	}

	// Error execution
	result, err = ExecuteWithResult(cb, context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("failure")
	})

	if err == nil {
		t.Error("Expected error")
	}
	if result != "" {
		t.Errorf("Expected empty result on error, got %q", result)
	}

	t.Log("✓ ExecuteWithResult works correctly")
}

// TestStateString verifies state string representation
func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.state.String()
		if result != tt.expected {
			t.Errorf("State %d: expected %q, got %q", tt.state, tt.expected, result)
		}
	}

	t.Log("✓ State string representation correct")
}
