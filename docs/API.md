# API Documentation

This document describes the key interfaces and types used in the CEX-DEX Arbitrage Detection Bot.

## Core Interfaces

### PriceProvider

The `PriceProvider` interface abstracts price fetching from different exchanges.

```go
// PriceProvider fetches prices from an exchange (CEX or DEX)
type PriceProvider interface {
    // GetPrice returns the execution price for a given trade size
    //
    // Parameters:
    //   - ctx: Context for cancellation and timeouts
    //   - size: Trade size in base token raw units (e.g., 1e18 for 1 ETH)
    //   - isBuy: true = buying base token, false = selling base token
    //   - gasPrice: Current gas price in wei (DEX only, nil for CEX)
    //   - blockNum: Current block number for cache keying
    //
    // Returns:
    //   - *Price: Execution details including effective price, slippage, fees
    //   - error: Any error during price fetching
    GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*Price, error)
}
```

**Implementations:**
- `pricing.BinanceProvider` - Binance orderbook integration
- `pricing.UniswapProvider` - Uniswap V3 QuoterV2 integration

### Cache

The `Cache` interface provides a generic caching abstraction.

```go
// Cache defines the caching interface
type Cache interface {
    // Get retrieves a value from cache
    // Returns ErrNotFound if key doesn't exist
    Get(ctx context.Context, key string) (interface{}, error)

    // Set stores a value with TTL
    Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

    // Delete removes a key from cache
    Delete(ctx context.Context, key string) error

    // Close releases cache resources
    Close() error
}

// ErrNotFound is returned when a cache key is not found
var ErrNotFound = errors.New("cache: key not found")
```

**Implementations:**
- `cache.MemoryCache` - In-memory LRU cache (L1)
- `cache.RedisCache` - Redis-backed cache (L2)
- `cache.LayeredCache` - L1 + L2 composite cache

### NotificationPublisher

The `NotificationPublisher` interface handles opportunity alerts.

```go
// NotificationPublisher publishes arbitrage opportunities
type NotificationPublisher interface {
    // PublishOpportunity sends an opportunity to the notification channel
    PublishOpportunity(ctx context.Context, opp *Opportunity) error
}
```

**Implementations:**
- `notification.SNSPublisher` - AWS SNS publisher
- `notification.NoopPublisher` - No-op for testing

## Data Types

### Price

Represents a price quote from any exchange.

```go
// Price represents a price quote for a trade
type Price struct {
    // Value is the effective price per unit in quote currency
    // Example: 2500.50 means 1 ETH = 2500.50 USDC
    Value *big.Float

    // AmountOut is the actual output amount (normalized)
    // For buy: quote amount spent
    // For sell: quote amount received
    AmountOut *big.Float

    // AmountOutRaw is the raw output amount (with decimals)
    // Example: 2500500000 for 2500.50 USDC (6 decimals)
    AmountOutRaw *big.Int

    // Slippage is the price impact percentage
    // Example: 0.5 means 0.5% slippage
    Slippage *big.Float

    // GasCost is the estimated gas cost in wei (DEX only)
    GasCost *big.Int

    // TradingFee is the exchange fee percentage
    // Example: 0.001 for 0.1% Binance fee
    TradingFee *big.Float

    // FeeTier is the Uniswap V3 fee tier used (0 for CEX)
    // Values: 100 (0.01%), 500 (0.05%), 3000 (0.3%), 10000 (1%)
    FeeTier uint32

    // Token metadata
    BaseSymbol    string
    QuoteSymbol   string
    BaseDecimals  int
    QuoteDecimals int

    Timestamp time.Time
}
```

### Opportunity

Represents a detected arbitrage opportunity.

```go
// Opportunity represents a detected arbitrage opportunity
type Opportunity struct {
    OpportunityID string    // Unique identifier
    BlockNumber   uint64    // Ethereum block number
    Timestamp     time.Time // Detection timestamp
    TradingPair   string    // e.g., "ETH-USDC"

    // Direction of the arbitrage
    Direction Direction // CEXToDEX or DEXToCEX

    // Trade details
    TradeSizeBase *big.Float // Amount in base token
    BaseSymbol    string     // e.g., "ETH"
    QuoteSymbol   string     // e.g., "USDC"
    BaseDecimals  int

    // Prices
    CEXPrice *big.Float // Effective CEX price
    DEXPrice *big.Float // Effective DEX price

    // Profit metrics
    GrossProfitUSD *big.Float // Profit before costs
    NetProfitUSD   *big.Float // Profit after costs
    ProfitPct      *big.Float // Net profit percentage

    // Costs
    GasCostWei     *big.Int   // Gas cost in wei
    GasCostUSD     *big.Float // Gas cost in USD
    TradingFeesUSD *big.Float // Total trading fees

    // Execution plan
    ExecutionSteps []string // Step-by-step execution guide
    RiskFactors    []string // Identified risks
}

// Direction indicates arbitrage direction
type Direction int

const (
    CEXToDEX Direction = iota // Buy CEX, Sell DEX
    DEXToCEX                  // Buy DEX, Sell CEX
)

// IsProfitable returns true if net profit is positive
func (o *Opportunity) IsProfitable() bool

// String returns direction as string ("CEX_TO_DEX" or "DEX_TO_CEX")
func (d Direction) String() string
```

### Block

Represents an Ethereum block header.

```go
// Block represents an Ethereum block header
type Block struct {
    Number     *big.Int    // Block number
    Hash       common.Hash // Block hash
    Timestamp  uint64      // Unix timestamp
    ParentHash common.Hash // Parent block hash
}

// IsValid checks if block data is valid
func (b *Block) IsValid() bool
```

## Configuration

### Main Configuration

```go
// Config holds the application configuration
type Config struct {
    Arbitrage  ArbitrageConfig
    Ethereum   EthereumConfig
    Binance    BinanceConfig
    Cache      CacheConfig
    SNS        SNSConfig
    Logging    LoggingConfig
}

// ArbitrageConfig holds arbitrage detection settings
type ArbitrageConfig struct {
    Pairs              []string  // Trading pairs: ["ETH-USDC", "ETH-USDT"]
    TradeSizes         []string  // Sizes to check: ["1", "10", "100"]
    MinProfitThreshold float64   // Minimum profit %: 0.5 = 0.5%
    GasPrice           GasPriceConfig
}

// EthereumConfig holds Ethereum connection settings
type EthereumConfig struct {
    WebSocketURLs []string       // Primary WebSocket URLs
    RPCEndpoints  []RPCEndpoint  // HTTP RPC fallback endpoints
    QuoterAddress string         // Uniswap QuoterV2 address
}

// GasPriceConfig holds gas price caching settings
type GasPriceConfig struct {
    TTL                 time.Duration // Normal cache TTL (default: 12s)
    VolatileTTL         time.Duration // TTL during volatility (default: 3s)
    VolatilityThreshold float64       // % change to trigger volatile mode
}
```

### Environment Variables

All config values can be overridden with environment variables using the `ARB_` prefix:

| Variable | Description | Example |
|----------|-------------|---------|
| `ARB_ETHEREUM_WEBSOCKET_URLS` | WebSocket URLs (comma-separated) | `wss://mainnet.infura.io/ws/v3/KEY` |
| `ARB_ETHEREUM_RPC_ENDPOINTS_0_URL` | First HTTP RPC URL | `https://mainnet.infura.io/v3/KEY` |
| `ARB_ARBITRAGE_PAIRS` | Trading pairs | `ETH-USDC,ETH-USDT` |
| `ARB_ARBITRAGE_TRADE_SIZES` | Trade sizes | `1,10,100` |
| `ARB_ARBITRAGE_MIN_PROFIT_THRESHOLD` | Min profit % | `0.5` |

## Resilience Components

### CircuitBreaker

Implements the circuit breaker pattern.

```go
// CircuitBreakerConfig holds circuit breaker settings
type CircuitBreakerConfig struct {
    Name             string        // Identifier for metrics
    FailureThreshold int           // Failures before opening (default: 5)
    SuccessThreshold int           // Successes to close (default: 2)
    Timeout          time.Duration // Open state duration (default: 60s)
    OnStateChange    func(from, to State) // State change callback
}

// State represents circuit breaker state
type State int
const (
    StateClosed   State = iota // Normal operation
    StateOpen                  // Rejecting requests
    StateHalfOpen              // Testing recovery
)

// CircuitBreaker manages request flow based on failure rate
type CircuitBreaker struct { ... }

// Execute runs a function through the circuit breaker
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) error) error

// State returns current state
func (cb *CircuitBreaker) State() State
```

### RateLimiter

Token bucket rate limiter.

```go
// RateLimiter limits request rate using token bucket algorithm
type RateLimiter struct { ... }

// NewRateLimiterFromRPM creates a rate limiter from requests per minute
func NewRateLimiterFromRPM(rpm, burst int) *RateLimiter

// Wait blocks until a token is available or context is cancelled
func (rl *RateLimiter) Wait(ctx context.Context) error
```

### Retry

Retry with exponential backoff.

```go
// RetryConfig holds retry settings
type RetryConfig struct {
    MaxAttempts int           // Maximum retry attempts
    BaseDelay   time.Duration // Initial delay
    MaxDelay    time.Duration // Maximum delay cap
    Jitter      float64       // Jitter factor (0-1)
}

// RetryIf retries a function with backoff if error matches predicate
func RetryIf(ctx context.Context, cfg RetryConfig, shouldRetry func(error) bool, fn func(context.Context) error) error

// IsRetryable returns true for transient errors (network, timeout)
func IsRetryable(err error) bool
```

## HTTP Endpoints

### Health Check

```
GET /health

Response 200:
{
  "status": "healthy",
  "providers": {
    "binance": {
      "healthy": true,
      "last_success": "2024-01-15T14:23:45Z",
      "circuit_state": "closed"
    },
    "uniswap:ETH-USDC": {
      "healthy": true,
      "last_success": "2024-01-15T14:23:44Z",
      "circuit_state": "closed"
    }
  }
}
```

### Metrics

```
GET /metrics

# Prometheus format
arbitrage_opportunities_detected_total{pair="ETH-USDC",direction="CEX_TO_DEX",profitable="true"} 42
arbitrage_block_processing_duration_ms_bucket{le="100"} 150
arbitrage_cache_hits_total{provider="binance"} 1234
```

## Usage Examples

### Creating a Binance Provider

```go
provider, err := pricing.NewBinanceProvider(pricing.BinanceProviderConfig{
    BaseURL:      "https://api.binance.com",
    RateLimitRPM: 1200,
    Cache:        memoryCache,
    Logger:       logger,
    Metrics:      metrics,
    TradingFee:   0.001, // 0.1%
})
```

### Creating a Uniswap Provider

```go
provider, err := pricing.NewUniswapProvider(pricing.UniswapProviderConfig{
    Client:         ethClient,
    QuoterAddress:  "0x61fFE014bA17989E743c5F6cB21bF9697530B21e",
    Token0Address:  "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", // USDC
    Token1Address:  "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", // WETH
    Token0Decimals: 6,
    Token1Decimals: 18,
    PairName:       "ETH-USDC",
    FeeTiers:       []uint32{500, 3000}, // 0.05% and 0.3%
    Cache:          layeredCache,
    Logger:         logger,
})
```

### Creating a Detector

```go
detector, err := arbitrage.NewDetector(arbitrage.DetectorConfig{
    CEXProvider:         cexAdapter,
    DEXProvider:         dexAdapter,
    Publisher:           snsPublisher,
    Cache:               layeredCache,
    Logger:              logger,
    Metrics:             metrics,
    TradeSizes:          []*big.Int{oneETH, tenETH},
    MinProfitPct:        0.5,
    ETHPriceUSD:         2500.0,
    PairName:            "ETH-USDC",
    BaseToken:           ethToken,
    QuoteToken:          usdcToken,
    PipelineConcurrency: 4,
})
```

### Subscribing to Blocks

```go
subscriber, _ := blockchain.NewSubscriber(blockchain.SubscriberConfig{
    WebSocketURLs:     []string{"wss://mainnet.infura.io/ws/v3/KEY"},
    Logger:            logger,
    ClientPool:        httpClientPool, // For HTTP fallback
    HeartbeatInterval: 30 * time.Second,
    MessageTimeout:    60 * time.Second,
})

blocks, errors, _ := subscriber.Subscribe(ctx)

for {
    select {
    case block := <-blocks:
        opportunities, _ := detector.Detect(ctx, block.Number.Uint64(), block.Timestamp)
        // Process opportunities...
    case err := <-errors:
        log.Printf("subscription error: %v", err)
    }
}
```
