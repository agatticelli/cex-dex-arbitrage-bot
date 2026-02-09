package arbitrage

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/config"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/pricing"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// PriceProvider defines the interface for fetching prices
// Interfaces defined where they're consumed (Dependency Inversion Principle)
type PriceProvider interface {
	GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error)
}

// ETHPriceProvider is an optional interface for providers that can fetch ETH/USD price
type ETHPriceProvider interface {
	GetETHPrice(ctx context.Context) (float64, error)
}

// NotificationPublisher defines the interface for publishing opportunities
type NotificationPublisher interface {
	PublishOpportunity(ctx context.Context, opp *Opportunity) error
}

// CacheStore defines the interface for caching
type CacheStore interface {
	Get(ctx context.Context, key string) (interface{}, error)
	Set(ctx context.Context, key string, val interface{}, ttl time.Duration) error
}

// Detector detects arbitrage opportunities between CEX and DEX
type Detector struct {
	cexProvider     PriceProvider
	dexProvider     PriceProvider
	publisher       NotificationPublisher
	calculator      *Calculator
	cache           CacheStore
	logger          *observability.Logger
	metrics         *observability.Metrics
	tradeSizes      []*big.Int
	minProfitPct    float64
	ethPriceUSD     float64
	tradingSymbol   string
	dexQuoteLimiter *semaphore.Weighted

	// Multi-pair support
	pairName   string           // "ETH-USDC"
	baseToken  config.TokenInfo // Base token (ETH, BTC, etc.)
	quoteToken config.TokenInfo // Quote token (USDC, USDT, etc.)

	// Gas price caching (reduce eth_gasPrice calls from 6/block to 1/12s)
	ethClient      *ethclient.Client
	cachedGasPrice *big.Int
	gasPriceMu     sync.RWMutex
	gasPriceExpiry time.Time
	gasPriceTTL    time.Duration
	maxGasPrice    *big.Int // Safety cap (500 gwei)
}

// DetectorConfig holds detector configuration
type DetectorConfig struct {
	CEXProvider   PriceProvider
	DEXProvider   PriceProvider
	Publisher     NotificationPublisher
	Cache         CacheStore
	Logger        *observability.Logger
	Metrics       *observability.Metrics
	TradeSizes    []*big.Int
	MinProfitPct  float64
	ETHPriceUSD   float64
	TradingSymbol string
	EthClient     *ethclient.Client // For gas price fetching
	// DEX quote concurrency limiter (shared across detectors)
	DEXQuoteLimiter *semaphore.Weighted

	// Multi-pair support
	PairName   string           // "ETH-USDC", "BTC-USDC", etc.
	BaseToken  config.TokenInfo // Base token metadata
	QuoteToken config.TokenInfo // Quote token metadata
}

// NewDetector creates a new arbitrage detector
func NewDetector(cfg DetectorConfig) (*Detector, error) {
	if cfg.CEXProvider == nil {
		return nil, fmt.Errorf("CEX provider is required")
	}
	if cfg.DEXProvider == nil {
		return nil, fmt.Errorf("DEX provider is required")
	}
	if cfg.Publisher == nil {
		return nil, fmt.Errorf("publisher is required")
	}

	// Set defaults
	if cfg.MinProfitPct == 0 {
		cfg.MinProfitPct = 0.5 // 0.5% minimum profit
	}
	if cfg.ETHPriceUSD == 0 {
		cfg.ETHPriceUSD = 2000 // Default ETH price
	}

	// Set max gas price to 500 gwei (safety cap)
	maxGasPrice := new(big.Int)
	maxGasPrice.SetString("500000000000", 10) // 500 gwei

	return &Detector{
		cexProvider:     cfg.CEXProvider,
		dexProvider:     cfg.DEXProvider,
		publisher:       cfg.Publisher,
		calculator:      NewCalculator(),
		cache:           cfg.Cache,
		logger:          cfg.Logger,
		metrics:         cfg.Metrics,
		tradeSizes:      cfg.TradeSizes,
		minProfitPct:    cfg.MinProfitPct,
		ethPriceUSD:     cfg.ETHPriceUSD,
		tradingSymbol:   cfg.TradingSymbol,
		dexQuoteLimiter: cfg.DEXQuoteLimiter,

		// Multi-pair support
		pairName:   cfg.PairName,
		baseToken:  cfg.BaseToken,
		quoteToken: cfg.QuoteToken,

		// Gas price caching
		ethClient:   cfg.EthClient,
		gasPriceTTL: 12 * time.Second, // Cache for ~1 block
		maxGasPrice: maxGasPrice,
	}, nil
}

// getGasPrice fetches the current gas price with 12-second caching
// This reduces eth_gasPrice calls from 6/block to 1 every 12 seconds
func (d *Detector) getGasPrice(ctx context.Context) (*big.Int, error) {
	// Return nil if no eth client configured (graceful degradation)
	if d.ethClient == nil {
		return nil, nil
	}

	d.gasPriceMu.RLock()
	// Check if cached and not expired
	if d.cachedGasPrice != nil && time.Now().Before(d.gasPriceExpiry) {
		gasPrice := new(big.Int).Set(d.cachedGasPrice)
		d.gasPriceMu.RUnlock()
		d.logger.Debug("using cached gas price",
			"gas_price_wei", gasPrice.String(),
			"ttl_remaining", time.Until(d.gasPriceExpiry).Seconds())
		return gasPrice, nil
	}
	d.gasPriceMu.RUnlock()

	// Fetch fresh gas price
	d.gasPriceMu.Lock()
	defer d.gasPriceMu.Unlock()

	// Double-check (another goroutine might have fetched)
	if d.cachedGasPrice != nil && time.Now().Before(d.gasPriceExpiry) {
		return new(big.Int).Set(d.cachedGasPrice), nil
	}

	// Fetch from network
	gasPrice, err := d.ethClient.SuggestGasPrice(ctx)
	if err != nil {
		d.logger.Warn("failed to fetch gas price, will retry next block", "error", err)
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Apply safety cap (max 500 gwei)
	if gasPrice.Cmp(d.maxGasPrice) > 0 {
		d.logger.Warn("gas price exceeds max, capping",
			"actual_wei", gasPrice.String(),
			"max_wei", d.maxGasPrice.String())
		gasPrice = new(big.Int).Set(d.maxGasPrice)
	}

	// Cache for 12 seconds
	d.cachedGasPrice = gasPrice
	d.gasPriceExpiry = time.Now().Add(d.gasPriceTTL)

	gasPriceGwei := new(big.Float).Quo(
		new(big.Float).SetInt(gasPrice),
		big.NewFloat(1e9),
	)
	gasPriceGweiFloat, _ := gasPriceGwei.Float64()

	d.logger.Info("fetched fresh gas price",
		"gas_price_wei", gasPrice.String(),
		"gas_price_gwei", gasPriceGweiFloat,
		"cached_for", d.gasPriceTTL.Seconds())

	return gasPrice, nil
}

// Detect detects arbitrage opportunities for a given block
func (d *Detector) Detect(ctx context.Context, blockNum uint64, blockTime uint64) ([]*Opportunity, error) {
	start := time.Now()

	d.logger.Info("detecting arbitrage opportunities",
		"block_number", blockNum,
		"block_time", blockTime,
		"trade_sizes", len(d.tradeSizes),
	)

	// Fetch latest ETH price from Binance
	latestETHPrice := d.fetchLatestETHPrice(ctx)
	if latestETHPrice != d.ethPriceUSD {
		d.UpdateETHPrice(latestETHPrice)
	}

	// Fetch gas price ONCE per block (cached for 12s)
	// This reduces eth_gasPrice calls from 6/block to 1 every 12 seconds
	gasPrice, err := d.getGasPrice(ctx)
	if err != nil {
		d.logger.Warn("failed to get gas price, continuing with default estimate", "error", err)
		// Continue without gas price - UniswapProvider will use default estimate
	}

	var opportunities []*Opportunity

	// Detect opportunities for each trade size
	for _, tradeSize := range d.tradeSizes {
		opps, err := d.detectForSize(ctx, blockNum, tradeSize, gasPrice)
		if err != nil {
			d.logger.LogError(ctx, "failed to detect opportunities for size", err,
				"block_number", blockNum,
				"trade_size", tradeSize.String(),
			)
			continue
		}

		opportunities = append(opportunities, opps...)
	}

	// Publish profitable opportunities
	publishedCount := 0
	for _, opp := range opportunities {
		if opp.IsProfitable() && opp.ProfitPct.Cmp(big.NewFloat(d.minProfitPct)) >= 0 {
			if err := d.publisher.PublishOpportunity(ctx, opp); err != nil {
				d.logger.LogError(ctx, "failed to publish opportunity", err,
					"opportunity_id", opp.OpportunityID,
				)
			} else {
				publishedCount++
			}
		}
	}

	// Record metrics
	duration := time.Since(start)
	if d.metrics != nil {
		d.metrics.RecordBlockProcessing(ctx, duration)
		for _, opp := range opportunities {
			profitUSD, _ := opp.NetProfitUSD.Float64()
			d.metrics.RecordOpportunity(ctx, d.pairName, opp.Direction.String(), opp.IsProfitable(), profitUSD)
		}
	}

	d.logger.Info("detection completed",
		"block_number", blockNum,
		"opportunities_found", len(opportunities),
		"published", publishedCount,
		"duration_ms", duration.Milliseconds(),
	)

	return opportunities, nil
}

// detectForSize detects opportunities for a specific trade size
func (d *Detector) detectForSize(ctx context.Context, blockNum uint64, tradeSize *big.Int, gasPrice *big.Int) ([]*Opportunity, error) {
	// Fetch prices from CEX and DEX in parallel
	var cexBuyPrice, cexSellPrice, dexBuyPrice, dexSellPrice *pricing.Price

	g, gctx := errgroup.WithContext(ctx)

	// Fetch CEX buy price (when we BUY base on CEX)
	// isBuy=true → uses Asks (sell orders, higher prices we pay to buy)
	// CEX doesn't use gas price or block number
	g.Go(func() error {
		price, err := d.cexProvider.GetPrice(gctx, tradeSize, true, nil, blockNum)
		if err != nil {
			return fmt.Errorf("CEX buy price: %w", err)
		}
		cexBuyPrice = price
		return nil
	})

	// Fetch CEX sell price (when we SELL base on CEX)
	// isBuy=false → uses Bids (buy orders, lower prices we receive when selling)
	// CEX doesn't use gas price or block number
	g.Go(func() error {
		price, err := d.cexProvider.GetPrice(gctx, tradeSize, false, nil, blockNum)
		if err != nil {
			return fmt.Errorf("CEX sell price: %w", err)
		}
		cexSellPrice = price
		return nil
	})

	// Fetch DEX buy price (when we BUY base on DEX)
	// isToken0In=true → swap quote (token0) for base (token1)
	// Pass cached gas price and block number (for caching)
	g.Go(func() error {
		if d.dexQuoteLimiter != nil {
			if err := d.dexQuoteLimiter.Acquire(gctx, 1); err != nil {
				return fmt.Errorf("DEX buy price limiter: %w", err)
			}
			defer d.dexQuoteLimiter.Release(1)
		}
		price, err := d.dexProvider.GetPrice(gctx, tradeSize, true, gasPrice, blockNum)
		if err != nil {
			return fmt.Errorf("DEX buy price: %w", err)
		}
		dexBuyPrice = price
		return nil
	})

	// Fetch DEX sell price (when we SELL base on DEX)
	// isToken0In=false → swap base (token1) for quote (token0)
	// Pass cached gas price and block number (for caching)
	g.Go(func() error {
		if d.dexQuoteLimiter != nil {
			if err := d.dexQuoteLimiter.Acquire(gctx, 1); err != nil {
				return fmt.Errorf("DEX sell price limiter: %w", err)
			}
			defer d.dexQuoteLimiter.Release(1)
		}
		price, err := d.dexProvider.GetPrice(gctx, tradeSize, false, gasPrice, blockNum)
		if err != nil {
			return fmt.Errorf("DEX sell price: %w", err)
		}
		dexSellPrice = price
		return nil
	})

	// Wait for all fetches to complete
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("failed to fetch prices: %w", err)
	}

	var opportunities []*Opportunity

	// Analyze CEX → DEX opportunity (buy on CEX, sell on DEX)
	if oppCEXToDEX := d.analyzeOpportunity(ctx, blockNum, tradeSize, CEXToDEX, cexBuyPrice, dexSellPrice); oppCEXToDEX != nil {
		opportunities = append(opportunities, oppCEXToDEX)
	}

	// Analyze DEX → CEX opportunity (buy on DEX, sell on CEX)
	if oppDEXToCEX := d.analyzeOpportunity(ctx, blockNum, tradeSize, DEXToCEX, dexBuyPrice, cexSellPrice); oppDEXToCEX != nil {
		opportunities = append(opportunities, oppDEXToCEX)
	}

	return opportunities, nil
}

// analyzeOpportunity analyzes a single arbitrage opportunity
func (d *Detector) analyzeOpportunity(
	ctx context.Context,
	blockNum uint64,
	tradeSize *big.Int,
	direction Direction,
	buyPrice *pricing.Price,
	sellPrice *pricing.Price,
) *Opportunity {
	// Calculate profit
	// IMPORTANT: Calculator expects (cexPrice, dexPrice), not (buyPrice, sellPrice)
	// We need to map correctly based on direction:
	var cexPrice, dexPrice *pricing.Price
	if direction == CEXToDEX {
		// Buy on CEX, Sell on DEX
		cexPrice = buyPrice  // CEX is where we buy
		dexPrice = sellPrice // DEX is where we sell
	} else {
		// Buy on DEX, Sell on CEX
		cexPrice = sellPrice // CEX is where we sell
		dexPrice = buyPrice  // DEX is where we buy
	}

	profitMetrics, err := d.calculator.CalculateProfit(direction, tradeSize, cexPrice, dexPrice, d.ethPriceUSD)
	if err != nil {
		d.logger.LogError(ctx, "failed to calculate profit", err,
			"direction", direction.String(),
			"trade_size", tradeSize.String(),
		)
		return nil
	}

	// Create opportunity
	opp := NewOpportunity(blockNum, direction, tradeSize)
	opp.TradingPair = d.pairName
	opp.SetBaseInfo(d.baseToken.Symbol, d.quoteToken.Symbol, d.baseToken.Decimals)

	// Set prices (use cexPrice, dexPrice - already mapped correctly above)
	opp.SetPrices(cexPrice.Value, dexPrice.Value)

	// Set profit metrics
	opp.SetProfitMetrics(
		profitMetrics.GrossProfitUSD,
		profitMetrics.NetProfitUSD,
		profitMetrics.ProfitPct,
	)

	// Set costs (gas is only paid on DEX side)
	var gasCost *big.Int
	if direction == CEXToDEX {
		// Buy on CEX (no gas), Sell on DEX (pay gas)
		gasCost = sellPrice.GasCost
	} else {
		// Buy on DEX (pay gas), Sell on CEX (no gas)
		gasCost = buyPrice.GasCost
	}
	opp.SetCosts(gasCost, profitMetrics.GasCostUSD, profitMetrics.TradingFeesUSD)

	// Add execution steps
	if direction == CEXToDEX {
		opp.AddExecutionStep(fmt.Sprintf("Buy %s %s on Binance at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, buyPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Transfer %s to wallet (if needed)", opp.BaseSymbol))
		opp.AddExecutionStep(fmt.Sprintf("Sell %s %s on Uniswap at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, sellPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Pay gas cost: %s wei", gasCost.String()))
	} else {
		opp.AddExecutionStep(fmt.Sprintf("Buy %s %s on Uniswap at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, buyPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Pay gas cost: %s wei", gasCost.String()))
		opp.AddExecutionStep(fmt.Sprintf("Transfer %s to Binance (if needed)", opp.BaseSymbol))
		opp.AddExecutionStep(fmt.Sprintf("Sell %s %s on Binance at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, sellPrice.Value.Text('f', 2)))
	}

	// Add risk factors
	if buyPrice.Slippage.Cmp(big.NewFloat(0.5)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High buy slippage: %s%%", buyPrice.Slippage.Text('f', 2)))
	}
	if sellPrice.Slippage.Cmp(big.NewFloat(0.5)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High sell slippage: %s%%", sellPrice.Slippage.Text('f', 2)))
	}
	if profitMetrics.GasCostUSD.Cmp(big.NewFloat(50)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High gas cost: $%s", profitMetrics.GasCostUSD.Text('f', 2)))
	}
	if direction == CEXToDEX || direction == DEXToCEX {
		opp.AddRiskFactor("Requires cross-exchange transfer (time risk)")
	}

	// Log opportunity with debug fields
	if opp.IsProfitable() {
		// Log basic opportunity info
		d.logger.Info("profitable opportunity found",
			"opportunity_id", opp.OpportunityID,
			"direction", direction.String(),
			"net_profit_usd", profitMetrics.NetProfitUSD.Text('f', 2),
			"profit_pct", profitMetrics.ProfitPct.Text('f', 4),
		)

		// Log detailed debug fields for profit calculation validation
		if profitMetrics.DebugFields != nil {
			d.logger.Info("profit calculation debug",
				"trade_size_base", profitMetrics.DebugFields["trade_size_base"],
				"cex_price", profitMetrics.DebugFields["cex_price"],
				"dex_price", profitMetrics.DebugFields["dex_price"],
				"usdc_spent", profitMetrics.DebugFields["usdc_spent"],
				"usdc_received", profitMetrics.DebugFields["usdc_received"],
				"cex_amount_out_normalized", profitMetrics.DebugFields["cex_amount_out_normalized"],
				"dex_amount_out_normalized", profitMetrics.DebugFields["dex_amount_out_normalized"],
				"cex_amount_out_raw", profitMetrics.DebugFields["cex_amount_out_raw"],
				"dex_amount_out_raw", profitMetrics.DebugFields["dex_amount_out_raw"],
				"gross_profit_usdc", profitMetrics.DebugFields["gross_profit_usdc"],
				"gas_cost_usdc", profitMetrics.DebugFields["gas_cost_usdc"],
				"trading_fees_usdc", profitMetrics.DebugFields["trading_fees_usdc"],
				"net_profit_usdc", profitMetrics.DebugFields["net_profit_usdc"],
				"expected_profit_approx", profitMetrics.DebugFields["expected_profit_approx"],
			)
		}
	}

	return opp
}

// GetTradeSizes returns configured trade sizes
func (d *Detector) GetTradeSizes() []*big.Int {
	return d.tradeSizes
}

// UpdateETHPrice updates the ETH price used for calculations
func (d *Detector) UpdateETHPrice(priceUSD float64) {
	d.ethPriceUSD = priceUSD
	d.logger.Info("updated ETH price", "price_usd", priceUSD)

	// Record ETH price metric
	if d.metrics != nil {
		d.metrics.RecordETHPrice(context.Background(), priceUSD)
	}
}

// fetchLatestETHPrice fetches the latest ETH price from the CEX provider
// Returns the last known price if fetching fails or provider doesn't support it
func (d *Detector) fetchLatestETHPrice(ctx context.Context) float64 {
	// Check if CEX provider implements ETHPriceProvider interface
	ethProvider, ok := d.cexProvider.(ETHPriceProvider)
	if !ok {
		d.logger.Debug("CEX provider does not support ETH price fetching, using cached price", "cached_price", d.ethPriceUSD)
		return d.ethPriceUSD
	}

	price, err := ethProvider.GetETHPrice(ctx)
	if err != nil {
		d.logger.Warn("failed to fetch ETH price, using cached price",
			"error", err,
			"cached_price", d.ethPriceUSD)
		return d.ethPriceUSD
	}

	return price
}
