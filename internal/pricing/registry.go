// Package pricing provides price providers for CEX and DEX exchanges.
package pricing

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/ethereum/go-ethereum/ethclient"
)

// PriceProvider is the interface that all price providers must implement.
// This is defined here to avoid import cycles.
type PriceProvider interface {
	GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*Price, error)
}

// ExchangeFactory is a function that creates a price provider from configuration.
type ExchangeFactory func(cfg ProviderConfig) (PriceProvider, error)

// ProviderConfig holds common configuration for price providers.
type ProviderConfig struct {
	// Common fields
	Name           string
	PairName       string
	BaseSymbol     string
	QuoteSymbol    string
	BaseDecimals   int
	QuoteDecimals  int
	QuoteIsStable  bool
	Cache          *cache.LayeredCache
	Logger         *observability.Logger
	Metrics        *observability.Metrics

	// CEX-specific fields
	BaseURL        string
	RateLimitRPM   int
	RateLimitBurst int
	TradingFee     float64

	// DEX-specific fields
	EthClient      *ethclient.Client
	QuoterAddress  string
	Token0Address  string  // Quote token address
	Token1Address  string  // Base token address
	FeeTiers       []uint32
}

// ExchangeRegistry manages exchange provider factories.
// It allows dynamic registration and creation of price providers.
type ExchangeRegistry struct {
	factories map[string]ExchangeFactory
	mu        sync.RWMutex
}

// NewExchangeRegistry creates a new exchange registry with built-in providers.
func NewExchangeRegistry() *ExchangeRegistry {
	r := &ExchangeRegistry{
		factories: make(map[string]ExchangeFactory),
	}

	// Register built-in providers
	r.Register("binance", r.createBinanceProvider)
	r.Register("uniswap_v3", r.createUniswapProvider)

	return r
}

// Register adds a new exchange factory to the registry.
func (r *ExchangeRegistry) Register(name string, factory ExchangeFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Create creates a price provider using the registered factory.
func (r *ExchangeRegistry) Create(name string, cfg ProviderConfig) (PriceProvider, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown exchange type: %s (available: %v)", name, r.ListExchanges())
	}

	return factory(cfg)
}

// ListExchanges returns a list of registered exchange types.
func (r *ExchangeRegistry) ListExchanges() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// createBinanceProvider creates a Binance price provider.
func (r *ExchangeRegistry) createBinanceProvider(cfg ProviderConfig) (PriceProvider, error) {
	provider, err := NewBinanceProvider(BinanceProviderConfig{
		BaseURL:        cfg.BaseURL,
		RateLimitRPM:   cfg.RateLimitRPM,
		RateLimitBurst: cfg.RateLimitBurst,
		Cache:          cfg.Cache,
		Logger:         cfg.Logger,
		Metrics:        cfg.Metrics,
		TradingFee:     cfg.TradingFee,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Binance provider: %w", err)
	}

	// Wrap in adapter
	return &BinanceAdapter{
		Provider:      provider,
		Symbol:        cfg.PairName, // Will be formatted by adapter
		BaseSymbol:    cfg.BaseSymbol,
		QuoteSymbol:   cfg.QuoteSymbol,
		BaseDecimals:  cfg.BaseDecimals,
		QuoteDecimals: cfg.QuoteDecimals,
		QuoteIsStable: cfg.QuoteIsStable,
	}, nil
}

// createUniswapProvider creates a Uniswap V3 price provider.
func (r *ExchangeRegistry) createUniswapProvider(cfg ProviderConfig) (PriceProvider, error) {
	if cfg.EthClient == nil {
		return nil, fmt.Errorf("EthClient is required for Uniswap provider")
	}

	provider, err := NewUniswapProvider(UniswapProviderConfig{
		Client:         cfg.EthClient,
		QuoterAddress:  cfg.QuoterAddress,
		Token0Address:  cfg.Token0Address,
		Token1Address:  cfg.Token1Address,
		Token0Decimals: cfg.QuoteDecimals,
		Token1Decimals: cfg.BaseDecimals,
		PairName:       cfg.PairName,
		Cache:          cfg.Cache,
		Logger:         cfg.Logger,
		Metrics:        cfg.Metrics,
		FeeTiers:       cfg.FeeTiers,
		RateLimitRPM:   cfg.RateLimitRPM,
		RateLimitBurst: cfg.RateLimitBurst,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Uniswap provider: %w", err)
	}

	// Wrap in adapter
	return &UniswapAdapter{
		Provider:      provider,
		BaseSymbol:    cfg.BaseSymbol,
		QuoteSymbol:   cfg.QuoteSymbol,
		BaseDecimals:  cfg.BaseDecimals,
		QuoteDecimals: cfg.QuoteDecimals,
		QuoteIsStable: cfg.QuoteIsStable,
	}, nil
}

// DefaultRegistry is the global exchange registry instance.
var DefaultRegistry = NewExchangeRegistry()
