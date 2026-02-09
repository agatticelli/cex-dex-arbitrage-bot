package pricing

import "time"

// ProviderHealth represents the current health state of a pricing provider.
type ProviderHealth struct {
	Provider            string
	Pair                string
	LastSuccess         time.Time
	LastFailure         time.Time
	LastError           string
	LastDuration        time.Duration
	ConsecutiveFailures int
	CircuitState        string
}

// HealthProvider is implemented by providers that expose health status.
type HealthProvider interface {
	Health() ProviderHealth
}
