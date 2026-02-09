package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrCircuitOpen is returned when circuit breaker is open
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// State represents circuit breaker state
type State int

const (
	// StateClosed allows all requests
	StateClosed State = iota
	// StateOpen rejects all requests
	StateOpen
	// StateHalfOpen allows limited requests to test recovery
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name             string
	failureThreshold int
	successThreshold int
	timeout          time.Duration

	state         State
	failures      int
	successes     int
	lastFailTime  time.Time
	mu            sync.RWMutex
	onStateChange func(from, to State)
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	Name             string
	FailureThreshold int           // Number of failures before opening
	SuccessThreshold int           // Number of successes in half-open before closing
	Timeout          time.Duration // Time to wait before transitioning from open to half-open
	OnStateChange    func(from, to State)
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}

	return &CircuitBreaker{
		name:             cfg.Name,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		timeout:          cfg.Timeout,
		state:            StateClosed,
		onStateChange:    cfg.OnStateChange,
	}
}

// Execute executes a function through the circuit breaker
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) error) error {
	// Check if circuit breaker allows the request
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	// Execute function
	err := fn(ctx)

	// Record result
	cb.afterRequest(err)

	return err
}

// ExecuteWithResult executes a function with result through circuit breaker
// Note: This is a standalone generic function, not a method, as Go doesn't support generic methods
func ExecuteWithResult[T any](cb *CircuitBreaker, ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var result T

	// Check if circuit breaker allows the request
	if err := cb.beforeRequest(); err != nil {
		return result, err
	}

	// Execute function
	res, err := fn(ctx)

	// Record result
	cb.afterRequest(err)

	return res, err
}

// beforeRequest checks if request should be allowed
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		// Allow request
		return nil

	case StateOpen:
		// Check if timeout has elapsed
		if time.Since(cb.lastFailTime) > cb.timeout {
			// Transition to half-open
			cb.setState(StateHalfOpen)
			return nil
		}
		// Reject request
		return ErrCircuitOpen

	case StateHalfOpen:
		// Allow request (testing recovery)
		return nil

	default:
		return ErrCircuitOpen
	}
}

// afterRequest records the result of a request
func (cb *CircuitBreaker) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		// Ignore context cancellations/timeouts for breaker state
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		// Request failed
		cb.failures++
		cb.successes = 0
		cb.lastFailTime = time.Now()

		switch cb.state {
		case StateClosed:
			if cb.failures >= cb.failureThreshold {
				cb.setState(StateOpen)
			}
		case StateHalfOpen:
			// Failure in half-open state -> back to open
			cb.setState(StateOpen)
		}
	} else {
		// Request succeeded
		cb.successes++

		switch cb.state {
		case StateClosed:
			// Reset failure count on success
			cb.failures = 0

		case StateHalfOpen:
			if cb.successes >= cb.successThreshold {
				// Enough successes -> close circuit
				cb.setState(StateClosed)
				cb.failures = 0
			}
		}
	}
}

// setState transitions to a new state
func (cb *CircuitBreaker) setState(newState State) {
	oldState := cb.state
	cb.state = newState

	// Call state change callback
	if cb.onStateChange != nil {
		cb.onStateChange(oldState, newState)
	}
}

// State returns current state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// StateInt returns current state as int (for metrics)
func (cb *CircuitBreaker) StateInt() int64 {
	state := cb.State()
	return int64(state)
}

// Name returns circuit breaker name
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.setState(StateClosed)
	cb.failures = 0
	cb.successes = 0
}

// ForceOpen manually forces circuit breaker to open state
func (cb *CircuitBreaker) ForceOpen() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.setState(StateOpen)
	cb.lastFailTime = time.Now()
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() (state State, failures, successes int) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.failures, cb.successes
}
