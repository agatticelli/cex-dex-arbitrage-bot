package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/resilience"
)

// Price represents a price quote for a trade
type Price struct {
	Value       *big.Float // Effective price in quote currency (for display)
	AmountOut   *big.Float // Actual output amount in quote currency (USDC, normalized to human-readable)
	AmountOutRaw *big.Int  // Raw output amount (with decimals, e.g., 1e6 for USDC, 1e18 for ETH)
	Slippage    *big.Float // Slippage percentage
	GasCost     *big.Int   // Gas cost in wei (DEX only)
	TradingFee  *big.Float // Trading fee percentage
	Timestamp   time.Time
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

	// Create rate limiter
	rateLimiter := resilience.NewRateLimiterFromRPM(cfg.RateLimitRPM, cfg.RateLimitBurst)

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
	}, nil
}

// GetPrice fetches current price for a trading pair and trade size
func (b *BinanceProvider) GetPrice(ctx context.Context, symbol string, size *big.Int, isBuy bool) (*Price, error) {
	start := time.Now()

	// Convert size from wei to ETH (assuming 18 decimals)
	sizeFloat := new(big.Float).SetInt(size)
	ethSize := new(big.Float).Quo(sizeFloat, big.NewFloat(1e18))

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
	execResult, err := orderbook.CalculateExecution(ethSize, isBuy)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate execution: %w", err)
	}

	// For Binance (CEX), USDC uses 6 decimals (but our orderbook already uses normalized values)
	// CRITICAL: Apply trading fees to get actual USDC flow
	// - For BUY (buying ETH): AmountOut = USDC we SPEND = totalCost × (1 + fee)
	// - For SELL (selling ETH): AmountOut = USDC we RECEIVE = totalCost × (1 - fee)
	var amountOutUSDC *big.Float
	if isBuy {
		// When buying, we pay MORE (add fee)
		amountOutUSDC = new(big.Float).Mul(execResult.TotalCost, big.NewFloat(1+b.tradingFee))
	} else {
		// When selling, we receive LESS (subtract fee)
		amountOutUSDC = new(big.Float).Mul(execResult.TotalCost, big.NewFloat(1-b.tradingFee))
	}

	// Convert to raw format (1e6 for USDC)
	amountOutRaw := new(big.Float).Mul(amountOutUSDC, big.NewFloat(1e6))
	amountOutRawInt := new(big.Int)
	amountOutRaw.Int(amountOutRawInt)

	// Record metrics
	duration := time.Since(start)
	if b.metrics != nil {
		b.metrics.RecordCEXAPICall(ctx, "binance", "orderbook", "success", duration)
	}

	b.logger.Info("fetched Binance price",
		"symbol", symbol,
		"size_eth", ethSize.Text('f', 4),
		"is_buy", isBuy,
		"price", execResult.EffectivePrice.Text('f', 2),
		"slippage_pct", execResult.Slippage.Text('f', 4),
		"amount_out_usdc", amountOutUSDC.Text('f', 2),
		"total_cost_usdc", execResult.TotalCost.Text('f', 2),
		"total_filled_eth", execResult.TotalFilled.Text('f', 4),
		"duration_ms", duration.Milliseconds(),
	)

	return &Price{
		Value:        execResult.EffectivePrice,
		AmountOut:    amountOutUSDC,       // Normalized USDC amount
		AmountOutRaw: amountOutRawInt,     // Raw USDC (1e6 decimals)
		Slippage:     execResult.Slippage,
		GasCost:      big.NewInt(0),       // CEX has no gas cost
		TradingFee:   big.NewFloat(b.tradingFee),
		Timestamp:    time.Now(),
	}, nil
}

// GetOrderbook fetches orderbook snapshot from Binance
func (b *BinanceProvider) GetOrderbook(ctx context.Context, symbol string, depth int) (*Orderbook, error) {
	// Wait for rate limiter
	if err := b.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Build URL
	url := fmt.Sprintf("%s/api/v3/depth?symbol=%s&limit=%d", b.baseURL, symbol, depth)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := b.client.Do(req)
	if err != nil {
		if b.metrics != nil {
			b.metrics.RecordCEXAPICall(ctx, "binance", "orderbook", "error", 0)
		}
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if b.metrics != nil {
			b.metrics.RecordCEXAPICall(ctx, "binance", "orderbook", "error", 0)
		}
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

// parseOrderbook converts Binance API response to Orderbook
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
