package pricing

import "time"

// ProviderHealth represents the current health state of a pricing provider.
// It is used by the health check endpoints to determine overall system health.
type ProviderHealth struct {
	// Provider is the name of the provider (e.g., "binance", "uniswap")
	Provider string

	// Pair is the trading pair this provider is configured for (e.g., "ETH-USDC")
	Pair string

	// LastSuccess is the timestamp of the last successful API call
	LastSuccess time.Time

	// LastFailure is the timestamp of the last failed API call
	LastFailure time.Time

	// LastError contains the error message from the last failure, if any
	LastError string

	// LastDuration is the latency of the last API call
	LastDuration time.Duration

	// ConsecutiveFailures is the count of consecutive failed API calls
	ConsecutiveFailures int

	// CircuitState is the current state of the circuit breaker (Closed, Open, HalfOpen)
	CircuitState string
}

// HealthProvider defines the interface for providers that expose health status.
// Implementations should track success/failure metrics for their external API calls.
//
// Health status is used by:
//   - /health endpoint to report overall system health
//   - /ready endpoint to determine readiness for traffic
//   - Monitoring dashboards to visualize provider availability
type HealthProvider interface {
	// Health returns the current health status of the provider.
	// This method should be thread-safe and non-blocking.
	Health() ProviderHealth
}
