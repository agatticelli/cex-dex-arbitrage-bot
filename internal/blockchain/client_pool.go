package blockchain

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
)

// RPCEndpoint represents a single Ethereum RPC endpoint
type RPCEndpoint struct {
	URL     string
	Weight  int
	Client  *ethclient.Client
	healthy atomic.Bool
}

// ClientPool manages multiple RPC endpoints with health tracking and failover
type ClientPool struct {
	endpoints      []*RPCEndpoint
	current        int
	mu             sync.RWMutex
	logger         *observability.Logger
	metrics        *observability.Metrics
	healthCheckTTL time.Duration
}

// ClientPoolConfig holds client pool configuration
type ClientPoolConfig struct {
	Endpoints      []EndpointConfig
	Logger         *observability.Logger
	Metrics        *observability.Metrics
	HealthCheckTTL time.Duration
}

// EndpointConfig represents endpoint configuration
type EndpointConfig struct {
	URL    string
	Weight int
}

// NewClientPool creates a new RPC client pool with multiple endpoints
func NewClientPool(cfg ClientPoolConfig) (*ClientPool, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("at least one RPC endpoint is required")
	}

	// Default health check interval
	if cfg.HealthCheckTTL == 0 {
		cfg.HealthCheckTTL = 30 * time.Second
	}

	endpoints := make([]*RPCEndpoint, 0, len(cfg.Endpoints))

	for _, epCfg := range cfg.Endpoints {
		client, err := ethclient.Dial(epCfg.URL)
		if err != nil {
			cfg.Logger.LogError(context.Background(), "failed to connect to RPC endpoint", err,
				"url", epCfg.URL,
			)
			// Create endpoint but mark as unhealthy
			endpoint := &RPCEndpoint{
				URL:    epCfg.URL,
				Weight: epCfg.Weight,
				Client: nil,
			}
			endpoint.healthy.Store(false)
			endpoints = append(endpoints, endpoint)
			continue
		}

		endpoint := &RPCEndpoint{
			URL:    epCfg.URL,
			Weight: epCfg.Weight,
			Client: client,
		}
		endpoint.healthy.Store(true)
		endpoints = append(endpoints, endpoint)

		cfg.Logger.Info("connected to RPC endpoint",
			"url", epCfg.URL,
			"weight", epCfg.Weight,
		)
	}

	// Ensure at least one endpoint is healthy
	hasHealthy := false
	for _, ep := range endpoints {
		if ep.healthy.Load() {
			hasHealthy = true
			break
		}
	}

	if !hasHealthy {
		return nil, fmt.Errorf("no healthy RPC endpoints available")
	}

	pool := &ClientPool{
		endpoints:      endpoints,
		current:        0,
		logger:         cfg.Logger,
		metrics:        cfg.Metrics,
		healthCheckTTL: cfg.HealthCheckTTL,
	}

	// Start background health checks
	go pool.startHealthChecks(context.Background())

	return pool, nil
}

// GetClient returns the next healthy client using round-robin selection
func (cp *ClientPool) GetClient() (*ethclient.Client, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Try all endpoints starting from current position
	attempts := 0
	startIdx := cp.current

	for attempts < len(cp.endpoints) {
		endpoint := cp.endpoints[cp.current]

		// Move to next endpoint for next call
		cp.current = (cp.current + 1) % len(cp.endpoints)
		attempts++

		// Check if endpoint is healthy
		if endpoint.healthy.Load() && endpoint.Client != nil {
			return endpoint.Client, nil
		}
	}

	// All endpoints unhealthy
	cp.current = startIdx // Reset to original position
	return nil, fmt.Errorf("no healthy RPC endpoints available")
}

// GetClientByURL returns a specific client by URL
func (cp *ClientPool) GetClientByURL(url string) (*ethclient.Client, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	for _, endpoint := range cp.endpoints {
		if endpoint.URL == url && endpoint.healthy.Load() && endpoint.Client != nil {
			return endpoint.Client, nil
		}
	}

	return nil, fmt.Errorf("no healthy client found for URL: %s", url)
}

// MarkUnhealthy marks an endpoint as unhealthy
func (cp *ClientPool) MarkUnhealthy(url string) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	for _, endpoint := range cp.endpoints {
		if endpoint.URL == url {
			wasHealthy := endpoint.healthy.Swap(false)
			if wasHealthy {
				cp.logger.Warn("marking RPC endpoint as unhealthy",
					"url", url,
				)

				// Record metric
				if cp.metrics != nil {
					cp.metrics.RecordRPCEndpointHealth(context.Background(), url, false)
				}
			}
			return
		}
	}
}

// startHealthChecks runs periodic health checks on all endpoints
func (cp *ClientPool) startHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(cp.healthCheckTTL)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cp.checkAllEndpoints(ctx)
		}
	}
}

// checkAllEndpoints checks health of all endpoints
func (cp *ClientPool) checkAllEndpoints(ctx context.Context) {
	// Use longer timeout for health checks (RPC calls can be slow)
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cp.mu.RLock()
	endpoints := cp.endpoints
	cp.mu.RUnlock()

	for _, endpoint := range endpoints {
		go cp.checkEndpoint(checkCtx, endpoint)
	}
}

// checkEndpoint checks if an endpoint is healthy by fetching block number
func (cp *ClientPool) checkEndpoint(ctx context.Context, endpoint *RPCEndpoint) {
	// If client is nil, try to reconnect
	if endpoint.Client == nil {
		client, err := ethclient.Dial(endpoint.URL)
		if err != nil {
			endpoint.healthy.Store(false)
			if cp.metrics != nil {
				cp.metrics.RecordRPCEndpointHealth(ctx, endpoint.URL, false)
			}
			return
		}
		endpoint.Client = client
		cp.logger.Info("reconnected to RPC endpoint", "url", endpoint.URL)
	}

	// Try to fetch latest block number
	_, err := endpoint.Client.BlockNumber(ctx)
	if err != nil {
		// Check if error is temporary (context canceled/timeout)
		// Don't close client for temporary errors
		isTemporaryError := ctx.Err() != nil ||
			err.Error() == "context canceled" ||
			err.Error() == "context deadline exceeded"

		if !isTemporaryError {
			// Only mark unhealthy and close for real errors
			wasHealthy := endpoint.healthy.Swap(false)
			if wasHealthy {
				cp.logger.LogError(ctx, "RPC endpoint health check failed", err,
					"url", endpoint.URL,
				)
			}

			// Record metric
			if cp.metrics != nil {
				cp.metrics.RecordRPCEndpointHealth(ctx, endpoint.URL, false)
			}

			// Close unhealthy client for persistent errors
			if endpoint.Client != nil {
				endpoint.Client.Close()
				endpoint.Client = nil
			}
		} else {
			// Temporary error - keep client alive but log warning
			cp.logger.Debug("RPC health check had temporary error (keeping client alive)",
				"url", endpoint.URL,
				"error", err.Error())
		}
		return
	}

	// Mark as healthy
	wasUnhealthy := !endpoint.healthy.Swap(true)
	if wasUnhealthy {
		cp.logger.Info("RPC endpoint is now healthy",
			"url", endpoint.URL,
		)
	}

	// Record metric
	if cp.metrics != nil {
		cp.metrics.RecordRPCEndpointHealth(ctx, endpoint.URL, true)
	}
}

// GetHealthyEndpointCount returns the number of healthy endpoints
func (cp *ClientPool) GetHealthyEndpointCount() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	count := 0
	for _, endpoint := range cp.endpoints {
		if endpoint.healthy.Load() {
			count++
		}
	}

	return count
}

// GetEndpointStatus returns status of all endpoints
func (cp *ClientPool) GetEndpointStatus() map[string]bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	status := make(map[string]bool, len(cp.endpoints))
	for _, endpoint := range cp.endpoints {
		status[endpoint.URL] = endpoint.healthy.Load()
	}

	return status
}

// Close closes all client connections
func (cp *ClientPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	for _, endpoint := range cp.endpoints {
		if endpoint.Client != nil {
			endpoint.Client.Close()
		}
	}

	cp.logger.Info("closed all RPC client connections")
}

// BlockNumber returns the latest block number from a healthy endpoint
func (cp *ClientPool) BlockNumber(ctx context.Context) (uint64, error) {
	client, err := cp.GetClient()
	if err != nil {
		return 0, err
	}

	return client.BlockNumber(ctx)
}

// BlockByNumber returns a block by number from a healthy endpoint
func (cp *ClientPool) BlockByNumber(ctx context.Context, number *big.Int) (interface{}, error) {
	client, err := cp.GetClient()
	if err != nil {
		return nil, err
	}

	return client.BlockByNumber(ctx, number)
}
