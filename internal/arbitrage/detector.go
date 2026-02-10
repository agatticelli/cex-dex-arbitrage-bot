package arbitrage

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/config"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
)

// PriceProvider fetches execution prices from an exchange (CEX or DEX).
// Implementations must handle rate limiting, caching, and error recovery.
type PriceProvider interface {
	// GetPrice returns the execution price for a given trade.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeouts
	//   - size: Trade size in base token raw units (e.g., 1e18 for 1 ETH)
	//   - isBuy: true = buying base token, false = selling base token
	//   - gasPrice: Current gas price in wei (DEX only, nil for CEX)
	//   - blockNum: Current block number for cache keying
	//
	// Returns the execution details including effective price, slippage, and fees.
	GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error)
}

// ETHPriceProvider fetches the current ETH/USD price.
// Used for converting gas costs from wei to USD.
type ETHPriceProvider interface {
	// GetETHPrice returns the current ETH price in USD.
	// Implementations should cache the result to avoid excessive API calls.
	GetETHPrice(ctx context.Context) (float64, error)
}

// NotificationPublisher publishes detected arbitrage opportunities.
// Implementations may publish to SNS, SQS, webhooks, or other channels.
type NotificationPublisher interface {
	// PublishOpportunity sends an opportunity to the notification channel.
	// Should be non-blocking; implementations may queue messages internally.
	PublishOpportunity(ctx context.Context, opp *Opportunity) error
}

// Detector orchestrates arbitrage detection for a single trading pair.
// It coordinates price fetching from CEX and DEX providers, calculates
// profit opportunities, and publishes profitable opportunities.
//
// The detector is designed for concurrent use and processes one block at a time.
// For multiple trading pairs, create separate Detector instances.
type Detector struct {
	cexProvider  PriceProvider
	publisher    NotificationPublisher
	logger       *observability.Logger
	metrics      *observability.Metrics
	tracer       observability.Tracer
	tradeSizes   []*big.Int
	minProfitPct float64
	ethPriceUSD  float64
	ethPriceMu   sync.RWMutex

	pairName   string
	baseToken  config.TokenInfo
	quoteToken config.TokenInfo

	ethClient             *ethclient.Client
	cachedGasPrice        *big.Int
	previousGasPrice      *big.Int
	gasPriceMu            sync.RWMutex
	gasPriceExpiry        time.Time
	gasPriceTTL           time.Duration
	gasPriceTTLVolatile   time.Duration
	gasPriceVolatilityPct float64
	maxGasPrice           *big.Int

	pipeline *Pipeline
}

// DetectorConfig holds configuration for creating a Detector.
// Required fields: CEXProvider, DEXProvider, Publisher.
type DetectorConfig struct {
	CEXProvider         PriceProvider
	DEXProvider         PriceProvider
	Publisher           NotificationPublisher
	Logger              *observability.Logger
	Metrics             *observability.Metrics
	Tracer              observability.Tracer
	TradeSizes          []*big.Int
	MinProfitPct        float64
	ETHPriceUSD         float64
	EthClient           *ethclient.Client
	DEXQuoteLimiter     *semaphore.Weighted
	PairName            string
	BaseToken           config.TokenInfo
	QuoteToken          config.TokenInfo
	PipelineConcurrency int
	GasPriceConfig      config.GasPriceConfig
}

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

	if cfg.MinProfitPct == 0 {
		cfg.MinProfitPct = 0.5
	}
	if cfg.PipelineConcurrency <= 0 {
		cfg.PipelineConcurrency = 4
	}
	if cfg.Tracer == nil {
		cfg.Tracer = observability.NewNoopTracer()
	}
	if cfg.GasPriceConfig.TTL == 0 {
		cfg.GasPriceConfig.TTL = 12 * time.Second
	}
	if cfg.GasPriceConfig.VolatileTTL == 0 {
		cfg.GasPriceConfig.VolatileTTL = 3 * time.Second
	}
	if cfg.GasPriceConfig.VolatilityThreshold == 0 {
		cfg.GasPriceConfig.VolatilityThreshold = 0.20
	}

	maxGasPrice := new(big.Int)
	maxGasPrice.SetString("500000000000", 10) // 500 gwei

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider:     cfg.CEXProvider,
		DEXProvider:     cfg.DEXProvider,
		Logger:          cfg.Logger,
		Metrics:         cfg.Metrics,
		Tracer:          cfg.Tracer,
		DEXQuoteLimiter: cfg.DEXQuoteLimiter,
		Concurrency:     cfg.PipelineConcurrency,
		MinProfitPct:    cfg.MinProfitPct,
	})

	return &Detector{
		cexProvider:           cfg.CEXProvider,
		publisher:             cfg.Publisher,
		logger:                cfg.Logger,
		metrics:               cfg.Metrics,
		tracer:                cfg.Tracer,
		tradeSizes:            cfg.TradeSizes,
		minProfitPct:          cfg.MinProfitPct,
		ethPriceUSD:           cfg.ETHPriceUSD,
		pairName:              cfg.PairName,
		baseToken:             cfg.BaseToken,
		quoteToken:            cfg.QuoteToken,
		ethClient:             cfg.EthClient,
		gasPriceTTL:           cfg.GasPriceConfig.TTL,
		gasPriceTTLVolatile:   cfg.GasPriceConfig.VolatileTTL,
		gasPriceVolatilityPct: cfg.GasPriceConfig.VolatilityThreshold,
		maxGasPrice:           maxGasPrice,
		pipeline:              pipeline,
	}, nil
}

func (d *Detector) isGasPriceVolatile(newPrice *big.Int) bool {
	if d.previousGasPrice == nil || d.previousGasPrice.Cmp(big.NewInt(0)) == 0 {
		return false
	}

	diff := new(big.Int).Sub(newPrice, d.previousGasPrice)
	diff.Abs(diff)

	diffFloat := new(big.Float).SetInt(diff)
	prevFloat := new(big.Float).SetInt(d.previousGasPrice)
	pctChange := new(big.Float).Quo(diffFloat, prevFloat)

	pctChangeFloat, _ := pctChange.Float64()
	return pctChangeFloat >= d.gasPriceVolatilityPct
}

func (d *Detector) getGasPrice(ctx context.Context) (*big.Int, error) {
	if d.ethClient == nil {
		return nil, nil
	}

	d.gasPriceMu.RLock()
	if d.cachedGasPrice != nil && time.Now().Before(d.gasPriceExpiry) {
		gasPrice := new(big.Int).Set(d.cachedGasPrice)
		d.gasPriceMu.RUnlock()
		d.logger.Debug("using cached gas price",
			"gas_price_wei", gasPrice.String(),
			"ttl_remaining", time.Until(d.gasPriceExpiry).Seconds())
		return gasPrice, nil
	}
	d.gasPriceMu.RUnlock()

	d.gasPriceMu.Lock()
	defer d.gasPriceMu.Unlock()

	if d.cachedGasPrice != nil && time.Now().Before(d.gasPriceExpiry) {
		return new(big.Int).Set(d.cachedGasPrice), nil
	}

	gasPrice, err := d.ethClient.SuggestGasPrice(ctx)
	if err != nil {
		d.logger.Warn("failed to fetch gas price, will retry next block", "error", err)
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	if gasPrice.Cmp(d.maxGasPrice) > 0 {
		d.logger.Warn("gas price exceeds max, capping",
			"actual_wei", gasPrice.String(),
			"max_wei", d.maxGasPrice.String())
		gasPrice = new(big.Int).Set(d.maxGasPrice)
	}

	isVolatile := d.isGasPriceVolatile(gasPrice)
	ttl := d.gasPriceTTL
	if isVolatile {
		ttl = d.gasPriceTTLVolatile
	}

	d.previousGasPrice = new(big.Int).Set(gasPrice)
	d.cachedGasPrice = gasPrice
	d.gasPriceExpiry = time.Now().Add(ttl)

	gasPriceGwei := new(big.Float).Quo(
		new(big.Float).SetInt(gasPrice),
		big.NewFloat(1e9),
	)
	gasPriceGweiFloat, _ := gasPriceGwei.Float64()

	d.logger.Info("fetched fresh gas price",
		"gas_price_wei", gasPrice.String(),
		"gas_price_gwei", gasPriceGweiFloat,
		"is_volatile", isVolatile,
		"cached_for", ttl.Seconds())

	return gasPrice, nil
}

// Detect analyzes a block for arbitrage opportunities across all configured trade sizes.
// It fetches prices from both CEX and DEX, calculates potential profits accounting for
// gas costs and trading fees, and publishes profitable opportunities.
//
// Returns all detected opportunities (profitable or not) and any errors encountered.
// Opportunities meeting the minimum profit threshold are automatically published.
func (d *Detector) Detect(ctx context.Context, blockNum uint64, blockTime uint64) ([]*Opportunity, error) {
	ctx, span := d.tracer.StartSpan(ctx, "Detector.Detect",
		observability.WithAttributes(
			attribute.String("pair", d.pairName),
			attribute.Int64("block_number", int64(blockNum)),
			attribute.Int("trade_sizes_count", len(d.tradeSizes)),
		),
	)
	defer span.End()

	start := time.Now()

	d.logger.Info("detecting arbitrage opportunities",
		"block_number", blockNum,
		"block_time", blockTime,
		"trade_sizes", len(d.tradeSizes),
	)

	latestETHPrice := d.fetchLatestETHPrice(ctx)
	if latestETHPrice == 0 {
		err := fmt.Errorf("ETH price unavailable: cannot calculate gas costs")
		span.NoticeError(err)
		d.logger.Error("cannot detect arbitrage: ETH price is unavailable",
			"block_number", blockNum,
			"impact", "skipping this block due to inability to calculate gas costs accurately")
		return nil, err
	}
	if latestETHPrice != d.getETHPriceUSD() {
		d.UpdateETHPrice(latestETHPrice)
	}

	gasPrice, err := d.getGasPrice(ctx)
	if err != nil {
		d.logger.Warn("failed to get gas price, continuing with default estimate", "error", err)
	}

	opportunities := d.detectWithPipeline(ctx, blockNum, blockTime, gasPrice)

	publishedCount := 0
	for _, opp := range opportunities {
		if opp.IsProfitable() {
			attrs := []attribute.KeyValue{
				attribute.String("opportunity_id", opp.OpportunityID),
				attribute.String("direction", opp.Direction.String()),
			}
			if opp.ProfitPct != nil {
				attrs = append(attrs, attribute.String("profit_pct", opp.ProfitPct.Text('f', 4)))
			}
			span.AddEvent("profitable_opportunity", attrs...)
		}
		if opp.IsProfitable() && opp.ProfitPct.Cmp(big.NewFloat(d.minProfitPct)) >= 0 {
			if err := d.publisher.PublishOpportunity(ctx, opp); err != nil {
				span.NoticeError(err)
				d.logger.LogError(ctx, "failed to publish opportunity", err,
					"opportunity_id", opp.OpportunityID,
				)
			} else {
				publishedCount++
			}
		}
	}

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
	span.SetAttribute("opportunities_found", len(opportunities))
	span.SetAttribute("published", publishedCount)
	span.SetAttribute("duration_ms", duration.Milliseconds())

	return opportunities, nil
}

func (d *Detector) detectWithPipeline(ctx context.Context, blockNum uint64, blockTime uint64, gasPrice *big.Int) []*Opportunity {
	return d.pipeline.Process(
		ctx,
		blockNum,
		blockTime,
		d.tradeSizes,
		gasPrice,
		d.getETHPriceUSD(),
		d.pairName,
		d.baseToken.Symbol,
		d.baseToken.Decimals,
		d.quoteToken.Symbol,
	)
}

func (d *Detector) GetTradeSizes() []*big.Int {
	return d.tradeSizes
}

func (d *Detector) UpdateETHPrice(priceUSD float64) {
	d.ethPriceMu.Lock()
	d.ethPriceUSD = priceUSD
	d.ethPriceMu.Unlock()

	d.logger.Info("updated ETH price", "price_usd", priceUSD)

	if d.metrics != nil {
		d.metrics.RecordETHPrice(context.Background(), priceUSD)
	}
}

func (d *Detector) getETHPriceUSD() float64 {
	d.ethPriceMu.RLock()
	defer d.ethPriceMu.RUnlock()
	return d.ethPriceUSD
}

func (d *Detector) fetchLatestETHPrice(ctx context.Context) float64 {
	ethProvider, ok := d.cexProvider.(ETHPriceProvider)
	if !ok {
		cachedPrice := d.getETHPriceUSD()
		if cachedPrice == 0 {
			d.logger.Error("no ETH price available and CEX provider does not support ETH price fetching",
				"impact", "gas cost calculations will be incorrect")
			return 0
		}
		d.logger.Debug("CEX provider does not support ETH price fetching, using cached price", "cached_price", cachedPrice)
		return cachedPrice
	}

	price, err := ethProvider.GetETHPrice(ctx)
	if err != nil {
		cachedPrice := d.getETHPriceUSD()
		if cachedPrice == 0 {
			d.logger.Error("failed to fetch initial ETH price from CEX, no fallback available",
				"error", err,
				"impact", "gas cost calculations will be incorrect until price is fetched successfully")
			return 0
		}
		d.logger.Warn("failed to fetch ETH price, using cached price",
			"error", err,
			"cached_price", cachedPrice)
		return cachedPrice
	}

	return price
}

func (d *Detector) Close() {
	if d.pipeline != nil {
		d.pipeline.Close()
	}
}
