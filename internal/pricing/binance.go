package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/resilience"
)

// Price represents a price quote for a trade
type Price struct {
	Value         *big.Float // Effective price in quote currency (for display)
	AmountOut     *big.Float // Actual output amount in quote currency (normalized to human-readable)
	AmountOutRaw  *big.Int   // Raw output amount (with decimals, e.g., 1e6 for USDC)
	Slippage      *big.Float // Slippage percentage
	GasCost       *big.Int   // Gas cost in wei (DEX only)
	TradingFee    *big.Float // Trading fee percentage
	FeeTier       uint32     // Uniswap V3 fee tier used (100/500/3000/10000 bps), 0 for CEX
	BaseSymbol    string     // Base token symbol (e.g., ETH, BTC)
	QuoteSymbol   string     // Quote token symbol (e.g., USDC)
	BaseDecimals  int        // Base token decimals
	QuoteDecimals int        // Quote token decimals
	QuoteIsStable bool       // Whether quote token is a stablecoin (for USD conversion)
	Timestamp     time.Time
}

// BinanceProvider fetches prices from Binance exchange
type BinanceProvider struct {
	client      *http.Client
	baseURL     string
	rateLimiter *resilience.RateLimiter
	cache       cache.Cache
	logger      *observability.Logger
	metrics     *observability.Metrics
	tradingFee  float64 // Binance trading fee (0.1% = 0.001)
	retryCfg    resilience.RetryConfig
	cb          *resilience.CircuitBreaker

	healthMu sync.RWMutex
	health   ProviderHealth
}

// BinanceProviderConfig holds Binance provider configuration
type BinanceProviderConfig struct {
	BaseURL        string
	RateLimitRPM   int
	RateLimitBurst int
	Cache          cache.Cache
	Logger         *observability.Logger
	Metrics        *observability.Metrics
	TradingFee     float64
	RetryConfig    resilience.RetryConfig
	CircuitBreaker *resilience.CircuitBreaker
}

// BinanceOrderbookResponse represents Binance orderbook API response
type BinanceOrderbookResponse struct {
	LastUpdateID int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"` // [price, quantity]
	Asks         [][]string `json:"asks"` // [price, quantity]
}

// NewBinanceProvider creates a new Binance provider
func NewBinanceProvider(cfg BinanceProviderConfig) (*BinanceProvider, error) {
	// Set defaults
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.binance.com"
	}
	if cfg.RateLimitRPM == 0 {
		cfg.RateLimitRPM = 1200 // Binance limit
	}
	if cfg.RateLimitBurst == 0 {
		cfg.RateLimitBurst = 50
	}
	if cfg.TradingFee == 0 {
		cfg.TradingFee = 0.001 // 0.1% default
	}
	if cfg.RetryConfig.MaxAttempts == 0 {
		cfg.RetryConfig = resilience.RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   200 * time.Millisecond,
			MaxDelay:    2 * time.Second,
			Jitter:      0.2,
		}
	}

	// Create rate limiter
	rateLimiter := resilience.NewRateLimiterFromRPM(cfg.RateLimitRPM, cfg.RateLimitBurst)

	// Create circuit breaker if not provided
	cb := cfg.CircuitBreaker
	if cb == nil {
		cb = resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
			Name:             "binance",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			OnStateChange: func(from, to resilience.State) {
				if cfg.Metrics != nil {
					cfg.Metrics.SetCircuitBreakerState(context.Background(), "binance", int64(to))
				}
			},
		})
	}
	if cfg.Metrics != nil {
		cfg.Metrics.SetCircuitBreakerState(context.Background(), "binance", cb.StateInt())
	}

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	return &BinanceProvider{
		client:      httpClient,
		baseURL:     cfg.BaseURL,
		rateLimiter: rateLimiter,
		cache:       cfg.Cache,
		logger:      cfg.Logger,
		metrics:     cfg.Metrics,
		tradingFee:  cfg.TradingFee,
		retryCfg:    cfg.RetryConfig,
		cb:          cb,
		health: ProviderHealth{
			Provider: "binance",
		},
	}, nil
}

// GetPrice fetches current price for a trading pair and trade size
func (b *BinanceProvider) GetPrice(ctx context.Context, symbol string, size *big.Int, isBuy bool, baseDecimals, quoteDecimals int) (*Price, error) {
	start := time.Now()

	if baseDecimals == 0 {
		baseDecimals = 18
	}
	if quoteDecimals == 0 {
		quoteDecimals = 6
	}

	// Convert size from raw units to base token amount
	baseSize := rawToFloat(size, baseDecimals)

	// Try cache first
	cacheKey := fmt.Sprintf("binance:orderbook:%s", symbol)
	var orderbook *Orderbook

	if b.cache != nil {
		cached, err := b.cache.Get(ctx, cacheKey)
		if err == nil {
			if ob, ok := cached.(*Orderbook); ok {
				orderbook = ob
				b.logger.Info("cache hit for Binance orderbook", "symbol", symbol)
				if b.metrics != nil {
					b.metrics.RecordCacheHit(ctx, "binance")
				}
			}
		} else {
			if b.metrics != nil {
				b.metrics.RecordCacheMiss(ctx, "binance")
			}
		}
	}

	// Fetch orderbook if not cached
	if orderbook == nil {
		var err error
		orderbook, err = b.GetOrderbook(ctx, symbol, 100)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch orderbook: %w", err)
		}

		// Cache orderbook for 10 seconds
		if b.cache != nil {
			b.cache.Set(ctx, cacheKey, orderbook, 10*time.Second)
		}
	}

	// Calculate execution result with actual USDC flows
	execResult, err := orderbook.CalculateExecution(baseSize, isBuy)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate execution: %w", err)
	}

	// CRITICAL: Apply trading fees to get actual quote flow
	// - For BUY (buying base): AmountOut = quote we SPEND = totalCost × (1 + fee)
	// - For SELL (selling base): AmountOut = quote we RECEIVE = totalCost × (1 - fee)
	var amountOutQuote *big.Float
	if isBuy {
		// When buying, we pay MORE (add fee)
		amountOutQuote = new(big.Float).Mul(execResult.TotalCost, big.NewFloat(1+b.tradingFee))
	} else {
		// When selling, we receive LESS (subtract fee)
		amountOutQuote = new(big.Float).Mul(execResult.TotalCost, big.NewFloat(1-b.tradingFee))
	}

	// Convert to raw format using quote decimals
	amountOutRawInt := floatToRaw(amountOutQuote, quoteDecimals)

	// Record metrics
	duration := time.Since(start)
	if b.metrics != nil {
		b.metrics.RecordCEXAPICall(ctx, "binance", "orderbook", "success", duration)
	}

	b.logger.Info("fetched Binance price",
		"symbol", symbol,
		"size_base", baseSize.Text('f', 4),
		"is_buy", isBuy,
		"price", execResult.EffectivePrice.Text('f', 2),
		"slippage_pct", execResult.Slippage.Text('f', 4),
		"amount_out_quote", amountOutQuote.Text('f', 2),
		"total_cost_quote", execResult.TotalCost.Text('f', 2),
		"total_filled_base", execResult.TotalFilled.Text('f', 4),
		"duration_ms", duration.Milliseconds(),
	)

	return &Price{
		Value:         execResult.EffectivePrice,
		AmountOut:     amountOutQuote,  // Normalized quote amount
		AmountOutRaw:  amountOutRawInt, // Raw quote amount (quote decimals)
		Slippage:      execResult.Slippage,
		GasCost:       big.NewInt(0), // CEX has no gas cost
		TradingFee:    big.NewFloat(b.tradingFee),
		FeeTier:       0,
		BaseDecimals:  baseDecimals,
		QuoteDecimals: quoteDecimals,
		Timestamp:     time.Now(),
	}, nil
}

// GetETHPrice fetches real-time ETH/USD price from Binance orderbook
// Returns the mid price (average of best bid and ask)
func (b *BinanceProvider) GetETHPrice(ctx context.Context) (float64, error) {
	cacheKey := "binance:orderbook:ETHUSDC"

	var orderbook *Orderbook

	// Try cache (same 10s cache as GetPrice)
	if b.cache != nil {
		if cached, err := b.cache.Get(ctx, cacheKey); err == nil {
			if ob, ok := cached.(*Orderbook); ok {
				orderbook = ob
				b.logger.Debug("cache hit for ETH price", "symbol", "ETHUSDC")
			}
		}
	}

	// Fetch if not cached
	if orderbook == nil {
		var err error
		orderbook, err = b.GetOrderbook(ctx, "ETHUSDC", 5)
		if err != nil {
			return 0, fmt.Errorf("failed to fetch ETH orderbook: %w", err)
		}

		// Cache for 10 seconds (same as GetPrice)
		if b.cache != nil {
			b.cache.Set(ctx, cacheKey, orderbook, 10*time.Second)
		}
	}

	if !orderbook.IsValid() {
		return 0, fmt.Errorf("invalid ETH orderbook")
	}

	midPrice := orderbook.GetMidPrice()
	ethPrice, _ := midPrice.Float64()

	// Sanity check: ETH price should be between $500 and $10000
	if ethPrice < 500 || ethPrice > 10000 {
		return 0, fmt.Errorf("ETH price outside valid range: %.2f", ethPrice)
	}

	b.logger.Debug("fetched ETH price", "price_usd", ethPrice)

	return ethPrice, nil
}

// GetOrderbook fetches orderbook snapshot from Binance
func (b *BinanceProvider) GetOrderbook(ctx context.Context, symbol string, depth int) (*Orderbook, error) {
	return resilience.ExecuteWithResult(b.cb, ctx, func(ctx context.Context) (*Orderbook, error) {
		return resilience.RetryIfWithResult(ctx, b.retryCfg, resilience.IsRetryable, func(ctx context.Context) (*Orderbook, error) {
			// Wait for rate limiter
			if err := b.rateLimiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("rate limiter error: %w", err)
			}

			// Build URL
			url := fmt.Sprintf("%s/api/v3/depth?symbol=%s&limit=%d", b.baseURL, symbol, depth)

			start := time.Now()
			orderbook, err := b.fetchOrderbook(ctx, url)
			duration := time.Since(start)

			b.recordHealth(err, duration)

			if b.metrics != nil {
				status := "success"
				if err != nil {
					status = "error"
				}
				b.metrics.RecordCEXAPICall(ctx, "binance", "orderbook", status, duration)
			}

			return orderbook, err
		})
	})
}

func (b *BinanceProvider) fetchOrderbook(ctx context.Context, url string) (*Orderbook, error) {
	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp BinanceOrderbookResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to Orderbook
	orderbook, err := b.parseOrderbook(&apiResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse orderbook: %w", err)
	}

	return orderbook, nil
}

// Health returns the current health status of the Binance provider.
func (b *BinanceProvider) Health() ProviderHealth {
	b.healthMu.RLock()
	defer b.healthMu.RUnlock()
	h := b.health
	if b.cb != nil {
		h.CircuitState = b.cb.State().String()
	}
	return h
}

func (b *BinanceProvider) recordHealth(err error, duration time.Duration) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.health.LastDuration = duration
	if err == nil {
		b.health.LastSuccess = time.Now()
		b.health.LastError = ""
		b.health.ConsecutiveFailures = 0
		return
	}

	b.health.LastFailure = time.Now()
	b.health.LastError = err.Error()
	b.health.ConsecutiveFailures++
}

// parseOrderbook converts Binance API response to Orderbook
// Name returns the provider name for warmup logging.
func (b *BinanceProvider) Name() string {
	return "binance"
}

// Warmup pre-populates the cache with initial orderbook data.
// This implements the cache.WarmupProvider interface.
func (b *BinanceProvider) Warmup(ctx context.Context) error {
	// Warm up ETHUSDC orderbook (used for ETH price and trading)
	_, err := b.GetOrderbook(ctx, "ETHUSDC", 100)
	if err != nil {
		return fmt.Errorf("failed to warm ETHUSDC orderbook: %w", err)
	}

	b.logger.Info("Binance cache warmed successfully", "symbol", "ETHUSDC")
	return nil
}

// WarmupSymbols pre-populates the cache with orderbooks for multiple symbols.
func (b *BinanceProvider) WarmupSymbols(ctx context.Context, symbols []string) error {
	for _, symbol := range symbols {
		if _, err := b.GetOrderbook(ctx, symbol, 100); err != nil {
			b.logger.Warn("failed to warm orderbook", "symbol", symbol, "error", err)
			// Continue with other symbols
		}
	}
	return nil
}

func (b *BinanceProvider) parseOrderbook(apiResp *BinanceOrderbookResponse) (*Orderbook, error) {
	orderbook := &Orderbook{
		Bids:      make([]OrderbookLevel, 0, len(apiResp.Bids)),
		Asks:      make([]OrderbookLevel, 0, len(apiResp.Asks)),
		Timestamp: time.Now(),
	}

	// Parse bids
	for _, bid := range apiResp.Bids {
		if len(bid) != 2 {
			continue
		}

		price, err := strconv.ParseFloat(bid[0], 64)
		if err != nil {
			b.logger.Warn("failed to parse bid price", "price", bid[0], "error", err)
			continue
		}

		volume, err := strconv.ParseFloat(bid[1], 64)
		if err != nil {
			b.logger.Warn("failed to parse bid volume", "volume", bid[1], "error", err)
			continue
		}

		orderbook.Bids = append(orderbook.Bids, OrderbookLevel{
			Price:  big.NewFloat(price),
			Volume: big.NewFloat(volume),
		})
	}

	// Parse asks
	for _, ask := range apiResp.Asks {
		if len(ask) != 2 {
			continue
		}

		price, err := strconv.ParseFloat(ask[0], 64)
		if err != nil {
			b.logger.Warn("failed to parse ask price", "price", ask[0], "error", err)
			continue
		}

		volume, err := strconv.ParseFloat(ask[1], 64)
		if err != nil {
			b.logger.Warn("failed to parse ask volume", "volume", ask[1], "error", err)
			continue
		}

		orderbook.Asks = append(orderbook.Asks, OrderbookLevel{
			Price:  big.NewFloat(price),
			Volume: big.NewFloat(volume),
		})
	}

	return orderbook, nil
}
