package pricing

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/resilience"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.opentelemetry.io/otel/attribute"
)

// PoolState represents the current state of a Uniswap V3 pool
type PoolState struct {
	SqrtPriceX96 *big.Int // Current sqrt price
	Tick         int32    // Current tick
	Liquidity    *big.Int // Current liquidity
	FeeGrowth0   *big.Int // Fee growth for token0
	FeeGrowth1   *big.Int // Fee growth for token1
	TickSpacing  int32    // Tick spacing for this pool
	Timestamp    time.Time
}

// TickInfo represents information about a specific tick
type TickInfo struct {
	LiquidityGross *big.Int // Total liquidity referencing this tick
	LiquidityNet   *big.Int // Liquidity delta when tick is crossed
	Initialized    bool     // Whether this tick is initialized
}

// UniswapProvider fetches prices from Uniswap V3 using QuoterV2 contract
type UniswapProvider struct {
	client            *ethclient.Client
	quoterContract    *bind.BoundContract
	poolAddress       common.Address
	token0Address     common.Address // Quote token (e.g., USDC)
	token1Address     common.Address // Base token (e.g., WETH)
	token0Decimals    int            // Quote token decimals
	token1Decimals    int            // Base token decimals
	pairName          string         // e.g., "ETH-USDC"
	cache             cache.Cache
	logger            *observability.Logger
	metrics           *observability.Metrics
	tracer            observability.Tracer
	feeTiers          []uint32                // Multiple fee tiers to try (e.g., [100, 500, 3000, 10000])
	quoterRateLimiter *resilience.RateLimiter // Rate limiter for QuoterV2 calls
	retryCfg          resilience.RetryConfig
	cb                *resilience.CircuitBreaker

	healthMu sync.RWMutex
	health   ProviderHealth
}

// UniswapProviderConfig holds Uniswap provider configuration
type UniswapProviderConfig struct {
	Client         *ethclient.Client
	QuoterAddress  string // QuoterV2 contract address
	PoolAddress    string
	Token0Address  string // Quote token address (e.g., USDC)
	Token1Address  string // Base token address (e.g., WETH)
	Token0Decimals int    // Quote token decimals
	Token1Decimals int    // Base token decimals
	PairName       string // Pair name for metrics (e.g., "ETH-USDC")
	Cache          cache.Cache
	Logger         *observability.Logger
	Metrics        *observability.Metrics
	Tracer         observability.Tracer
	FeeTiers       []uint32 // Multiple fee tiers to try
	RateLimitRPM   int      // Rate limit: requests per minute for QuoterV2 calls (default 300)
	RateLimitBurst int      // Rate limit: burst size (default 20)
	RetryConfig    resilience.RetryConfig
	CircuitBreaker *resilience.CircuitBreaker
}

// Uniswap V3 QuoterV2 ABI (for accurate price quotes)
const uniswapV3QuoterABI = `[
	{
		"inputs": [
			{
				"components": [
					{"internalType": "address", "name": "tokenIn", "type": "address"},
					{"internalType": "address", "name": "tokenOut", "type": "address"},
					{"internalType": "uint256", "name": "amountIn", "type": "uint256"},
					{"internalType": "uint24", "name": "fee", "type": "uint24"},
					{"internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160"}
				],
				"internalType": "struct IQuoterV2.QuoteExactInputSingleParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "quoteExactInputSingle",
		"outputs": [
			{"internalType": "uint256", "name": "amountOut", "type": "uint256"},
			{"internalType": "uint160", "name": "sqrtPriceX96After", "type": "uint160"},
			{"internalType": "uint32", "name": "initializedTicksCrossed", "type": "uint32"},
			{"internalType": "uint256", "name": "gasEstimate", "type": "uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"inputs": [
			{
				"components": [
					{"internalType": "address", "name": "tokenIn", "type": "address"},
					{"internalType": "address", "name": "tokenOut", "type": "address"},
					{"internalType": "uint256", "name": "amount", "type": "uint256"},
					{"internalType": "uint24", "name": "fee", "type": "uint24"},
					{"internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160"}
				],
				"internalType": "struct IQuoterV2.QuoteExactOutputSingleParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "quoteExactOutputSingle",
		"outputs": [
			{"internalType": "uint256", "name": "amountIn", "type": "uint256"},
			{"internalType": "uint160", "name": "sqrtPriceX96After", "type": "uint160"},
			{"internalType": "uint32", "name": "initializedTicksCrossed", "type": "uint32"},
			{"internalType": "uint256", "name": "gasEstimate", "type": "uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	}
]`

// Uniswap V3 Pool ABI (methods needed for pool state)
const uniswapV3PoolABI = `[
	{
		"inputs": [],
		"name": "slot0",
		"outputs": [
			{"internalType": "uint160", "name": "sqrtPriceX96", "type": "uint160"},
			{"internalType": "int24", "name": "tick", "type": "int24"},
			{"internalType": "uint16", "name": "observationIndex", "type": "uint16"},
			{"internalType": "uint16", "name": "observationCardinality", "type": "uint16"},
			{"internalType": "uint16", "name": "observationCardinalityNext", "type": "uint16"},
			{"internalType": "uint8", "name": "feeProtocol", "type": "uint8"},
			{"internalType": "bool", "name": "unlocked", "type": "bool"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "liquidity",
		"outputs": [
			{"internalType": "uint128", "name": "", "type": "uint128"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType": "int16", "name": "wordPosition", "type": "int16"}
		],
		"name": "tickBitmap",
		"outputs": [
			{"internalType": "uint256", "name": "", "type": "uint256"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType": "int24", "name": "tick", "type": "int24"}
		],
		"name": "ticks",
		"outputs": [
			{"internalType": "uint128", "name": "liquidityGross", "type": "uint128"},
			{"internalType": "int128", "name": "liquidityNet", "type": "int128"},
			{"internalType": "uint256", "name": "feeGrowthOutside0X128", "type": "uint256"},
			{"internalType": "uint256", "name": "feeGrowthOutside1X128", "type": "uint256"},
			{"internalType": "int56", "name": "tickCumulativeOutside", "type": "int56"},
			{"internalType": "uint160", "name": "secondsPerLiquidityOutsideX128", "type": "uint160"},
			{"internalType": "uint32", "name": "secondsOutside", "type": "uint32"},
			{"internalType": "bool", "name": "initialized", "type": "bool"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "tickSpacing",
		"outputs": [
			{"internalType": "int24", "name": "", "type": "int24"}
		],
		"stateMutability": "view",
		"type": "function"
	}
]`

// NewUniswapProvider creates a new Uniswap V3 provider
func NewUniswapProvider(cfg UniswapProviderConfig) (*UniswapProvider, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("ethereum client is required")
	}

	if cfg.QuoterAddress == "" {
		return nil, fmt.Errorf("quoter address is required")
	}

	if cfg.Token0Address == "" || cfg.Token1Address == "" {
		return nil, fmt.Errorf("token addresses are required")
	}
	if cfg.Token0Decimals == 0 || cfg.Token1Decimals == 0 {
		return nil, fmt.Errorf("token decimals are required")
	}

	if len(cfg.FeeTiers) == 0 {
		cfg.FeeTiers = []uint32{3000} // Default 0.3% fee if not specified
	}
	if cfg.Tracer == nil {
		cfg.Tracer = observability.NewNoopTracer()
	}

	// Set rate limit defaults (conservative for Infura free tier)
	if cfg.RateLimitRPM == 0 {
		cfg.RateLimitRPM = 60 // Default 60 requests/minute (Infura free tier safe)
	}
	if cfg.RateLimitBurst == 0 {
		cfg.RateLimitBurst = 12 // Default burst of 12 (2 tiers × 2 directions × 3 sizes)
	}
	if cfg.RetryConfig.MaxAttempts == 0 {
		cfg.RetryConfig = resilience.RetryConfig{
			MaxAttempts: 2,
			BaseDelay:   200 * time.Millisecond,
			MaxDelay:    1 * time.Second,
			Jitter:      0.2,
		}
	}

	// Create rate limiter for QuoterV2 calls
	rateLimiter := resilience.NewRateLimiterFromRPM(cfg.RateLimitRPM, cfg.RateLimitBurst)

	// Create circuit breaker if not provided
	cb := cfg.CircuitBreaker
	serviceName := "uniswap"
	if cfg.PairName != "" {
		serviceName = fmt.Sprintf("uniswap:%s", cfg.PairName)
	}
	if cb == nil {
		cb = resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
			Name:             serviceName,
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			OnStateChange: func(from, to resilience.State) {
				if cfg.Metrics != nil {
					cfg.Metrics.SetCircuitBreakerState(context.Background(), serviceName, int64(to))
				}
			},
		})
	}
	if cfg.Metrics != nil {
		cfg.Metrics.SetCircuitBreakerState(context.Background(), serviceName, cb.StateInt())
	}

	// Parse addresses
	quoterAddr := common.HexToAddress(cfg.QuoterAddress)
	poolAddr := common.HexToAddress(cfg.PoolAddress)
	token0Addr := common.HexToAddress(cfg.Token0Address)
	token1Addr := common.HexToAddress(cfg.Token1Address)

	// Parse QuoterV2 ABI
	quoterABI, err := abi.JSON(strings.NewReader(uniswapV3QuoterABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse quoter ABI: %w", err)
	}

	// Create bound contract for QuoterV2
	quoterContract := bind.NewBoundContract(quoterAddr, quoterABI, cfg.Client, nil, nil)

	return &UniswapProvider{
		client:            cfg.Client,
		quoterContract:    quoterContract,
		poolAddress:       poolAddr,
		token0Address:     token0Addr,
		token1Address:     token1Addr,
		token0Decimals:    cfg.Token0Decimals,
		token1Decimals:    cfg.Token1Decimals,
		pairName:          cfg.PairName,
		cache:             cfg.Cache,
		logger:            cfg.Logger,
		metrics:           cfg.Metrics,
		tracer:            cfg.Tracer,
		feeTiers:          cfg.FeeTiers,
		quoterRateLimiter: rateLimiter,
		retryCfg:          cfg.RetryConfig,
		cb:                cb,
		health: ProviderHealth{
			Provider: "uniswap",
			Pair:     cfg.PairName,
		},
	}, nil
}

// GetPrice fetches current price from Uniswap V3 pool by trying all configured fee tiers
// and returning the best execution price
// gasPrice is optional - if nil, will fetch from network (expensive)
// If provided (cached from detector), uses it directly (efficient)
// blockNum is used for per-block caching to reduce RPC calls
func (u *UniswapProvider) GetPrice(ctx context.Context, size *big.Int, isToken0In bool, gasPrice *big.Int, blockNum uint64) (*Price, error) {
	direction := map[bool]string{true: "buy", false: "sell"}[isToken0In]
	ctx, span := u.tracer.StartSpan(
		ctx,
		"Uniswap.GetPrice",
		observability.WithAttributes(
			attribute.String("direction", direction),
			attribute.String("pair", u.pairName),
			attribute.String("size", size.String()),
		),
	)
	defer span.End()

	start := time.Now()

	// Try all fee tiers and pick the best execution price
	var bestPrice *Price
	var bestAmountOut *big.Float // For comparison

	for _, feeTier := range u.feeTiers {
		price, err := u.getPriceForFeeTier(ctx, size, isToken0In, gasPrice, feeTier, blockNum)
		if err != nil {
			u.logger.Warn("fee tier quote failed",
				"fee_tier", feeTier,
				"error", err,
			)
			continue // Try next tier
		}

		// Compare: pick best execution
		// - For buying (isToken0In=true): minimize USDC spent (lower is better)
		// - For selling (isToken0In=false): maximize USDC received (higher is better)
		if bestPrice == nil {
			bestPrice = price
			bestAmountOut = price.AmountOut
		} else {
			betterPrice := false
			if isToken0In {
				// Buying: lower USDC spent is better
				betterPrice = price.AmountOut.Cmp(bestAmountOut) < 0
			} else {
				// Selling: higher USDC received is better
				betterPrice = price.AmountOut.Cmp(bestAmountOut) > 0
			}

			if betterPrice {
				bestPrice = price
				bestAmountOut = price.AmountOut
			}
		}
	}

	if bestPrice == nil {
		err := fmt.Errorf("all fee tier quotes failed")
		span.NoticeError(err)
		return nil, err
	}
	span.SetAttribute("best_fee_tier", int64(bestPrice.FeeTier))

	// Record metrics for fee tier selection
	if u.metrics != nil {
		u.metrics.RecordFeeTierUsed(ctx, bestPrice.FeeTier)
		duration := time.Since(start)
		u.logger.Info("selected best fee tier",
			"fee_tier", bestPrice.FeeTier,
			"direction", map[bool]string{true: "buy", false: "sell"}[isToken0In],
			"price", bestPrice.Value.Text('f', 2),
			"amountOut", bestPrice.AmountOut.Text('f', 2),
			"duration_ms", duration.Milliseconds(),
		)
	}

	return bestPrice, nil
}

// getPriceForFeeTier fetches price for a specific fee tier with per-block caching
func (u *UniswapProvider) getPriceForFeeTier(ctx context.Context, size *big.Int, isToken0In bool, gasPrice *big.Int, feeTier uint32, blockNum uint64) (*Price, error) {
	direction := map[bool]string{true: "buy", false: "sell"}[isToken0In]
	ctx, span := u.tracer.StartSpan(
		ctx,
		"Uniswap.GetQuote",
		observability.WithAttributes(
			attribute.Int64("fee_tier", int64(feeTier)),
			attribute.String("direction", direction),
		),
	)
	defer span.End()

	start := time.Now()

	// Build cache key (includes block number for per-block TTL)
	cacheKey := u.buildQuoteCacheKey(blockNum, size, isToken0In, feeTier)

	// Try cache first
	if u.cache != nil {
		if cached, err := u.cache.Get(ctx, cacheKey); err == nil {
			if price, ok := cached.(*Price); ok {
				span.SetAttribute("cache_hit", true)
				setQuoteSpanAmounts(span, size, price, isToken0In)
				u.logger.Debug("cache hit for DEX quote",
					"block", blockNum,
					"fee_tier", feeTier,
					"direction", map[bool]string{true: "buy", false: "sell"}[isToken0In],
				)
				if u.metrics != nil {
					u.metrics.RecordCacheHit(ctx, "uniswap")
					u.metrics.RecordQuoteCacheRequest(ctx, u.pairName, feeTier, "uniswap", "L1", true)
				}
				return price, nil
			}
		} else {
			if u.metrics != nil {
				u.metrics.RecordCacheMiss(ctx, "uniswap")
				u.metrics.RecordQuoteCacheRequest(ctx, u.pairName, feeTier, "uniswap", "L1", false)
			}
		}
	}
	span.SetAttribute("cache_hit", false)

	// PRODUCTION-READY: Use QuoterV2 for accurate price quotes
	// QuoterV2 handles all the complex Uniswap V3 math internally:
	// - Tick-walking across multiple price ranges
	// - Exact liquidity calculations at each tick
	// - Proper handling of fees and slippage
	// - Gas estimation for the actual swap

	// Determine token addresses and call appropriate QuoterV2 method
	// Note: The `size` parameter is always in base token raw units for both directions
	var tokenIn, tokenOut common.Address
	var result []interface{}
	callOpts := &bind.CallOpts{Context: ctx}
	if blockNum > 0 {
		callOpts.BlockNumber = new(big.Int).SetUint64(blockNum)
	}

	// Track QuoterV2 call timing
	quoterStart := time.Now()
	var quoterErr error

	callQuoter := func(method string, params interface{}) error {
		if u.cb == nil {
			return fmt.Errorf("quoter circuit breaker not initialized")
		}
		callStart := time.Now()
		err := u.cb.Execute(ctx, func(ctx context.Context) error {
			return resilience.RetryIf(ctx, u.retryCfg, resilience.IsRetryable, func(ctx context.Context) error {
				// Wait for rate limiter before calling QuoterV2
				if err := u.quoterRateLimiter.Wait(ctx); err != nil {
					return fmt.Errorf("rate limiter: %w", err)
				}
				return u.quoterContract.Call(callOpts, &result, method, params)
			})
		})
		u.recordHealth(err, time.Since(callStart))
		return err
	}

	if isToken0In {
		// Buying base with quote: quote (token0) -> base (token1)
		// size is the base token amount we want to buy (raw units)
		// Use quoteExactOutputSingle to get required quote for exact base output

		tokenIn = u.token0Address
		tokenOut = u.token1Address

		params := struct {
			TokenIn           common.Address
			TokenOut          common.Address
			Amount            *big.Int
			Fee               *big.Int
			SqrtPriceLimitX96 *big.Int
		}{
			TokenIn:           tokenIn,
			TokenOut:          tokenOut,
			Amount:            size, // Desired base output
			Fee:               big.NewInt(int64(feeTier)),
			SqrtPriceLimitX96: big.NewInt(0),
		}

		quoterErr = callQuoter("quoteExactOutputSingle", params)
		if quoterErr != nil {
			span.NoticeError(quoterErr)
			// Record failed QuoterV2 call
			if u.metrics != nil {
				u.metrics.RecordQuoterCall(ctx, feeTier, "error", time.Since(quoterStart))
			}
			return nil, fmt.Errorf("QuoterV2 quoteExactOutputSingle failed: %w", quoterErr)
		}
	} else {
		// Selling base for quote: base (token1) -> quote (token0)
		// size is the base token amount we want to sell (raw units)
		// Use quoteExactInputSingle to get quote output for exact base input

		tokenIn = u.token1Address
		tokenOut = u.token0Address

		params := struct {
			TokenIn           common.Address
			TokenOut          common.Address
			AmountIn          *big.Int
			Fee               *big.Int
			SqrtPriceLimitX96 *big.Int
		}{
			TokenIn:           tokenIn,
			TokenOut:          tokenOut,
			AmountIn:          size, // Base input
			Fee:               big.NewInt(int64(feeTier)),
			SqrtPriceLimitX96: big.NewInt(0),
		}

		quoterErr = callQuoter("quoteExactInputSingle", params)
		if quoterErr != nil {
			span.NoticeError(quoterErr)
			// Record failed QuoterV2 call
			if u.metrics != nil {
				u.metrics.RecordQuoterCall(ctx, feeTier, "error", time.Since(quoterStart))
			}
			return nil, fmt.Errorf("QuoterV2 quoteExactInputSingle failed: %w", quoterErr)
		}
	}

	// Record successful QuoterV2 call
	if u.metrics != nil {
		u.metrics.RecordQuoterCall(ctx, feeTier, "success", time.Since(quoterStart))
	}

	// Parse results - format differs between quoteExactInputSingle and quoteExactOutputSingle
	// Both return: (amount, sqrtPriceX96After, initializedTicksCrossed, gasEstimate)
	firstAmount := result[0].(*big.Int)
	// sqrtPriceAfter := result[1].(*big.Int) // Not used for now
	ticksCrossed := result[2].(uint32)
	gasEstimate := result[3].(*big.Int)

	var amountOutQuote *big.Float
	var amountOutBase *big.Float // For logging
	var priceImpact PriceImpact

	if isToken0In {
		// Buying base with quote: used quoteExactOutputSingle
		// firstAmount is the quote input required
		// size is the base output (what we requested)
		amountOutBase = rawToFloat(size, u.token1Decimals)

		// Calculate price impact from actual amounts
		priceImpact = CalculatePriceImpact(
			firstAmount, size, // amountIn (quote), amountOut (base)
			u.token0Decimals, u.token1Decimals,
			true, // isBuying
			ticksCrossed,
			gasEstimate,
		)
		amountOutQuote = rawToFloat(firstAmount, u.token0Decimals)
	} else {
		// Selling base for quote: used quoteExactInputSingle
		// firstAmount is the quote output received
		// size is the base input
		amountOutQuote = rawToFloat(firstAmount, u.token0Decimals)

		// Calculate price impact from actual amounts
		priceImpact = CalculatePriceImpact(
			size, firstAmount, // amountIn (base), amountOut (quote)
			u.token1Decimals, u.token0Decimals,
			false, // isSelling
			ticksCrossed,
			gasEstimate,
		)
	}

	// Use effective price from price impact calculation
	effectivePrice := priceImpact.EffectivePrice
	slippagePct := priceImpact.ImpactBPS.Float64() / 100 // Convert BPS to percentage

	// Calculate gas cost
	var gasCost *big.Int
	if gasPrice != nil {
		// Use provided gas price with QuoterV2's gas estimate
		gasCost = new(big.Int).Mul(gasEstimate, gasPrice)
	} else {
		// Fallback: fetch gas price from network
		var err error
		gasCost, err = u.EstimateGasCost(ctx, size, isToken0In)
		if err != nil {
			span.NoticeError(err)
			u.logger.Warn("failed to estimate gas cost", "error", err)
			// Use QuoterV2's gas estimate with default 50 gwei
			gasCost = new(big.Int).Mul(gasEstimate, big.NewInt(50e9))
		}
	}

	// Record metrics
	duration := time.Since(start)
	if u.metrics != nil {
		u.metrics.RecordDEXQuote(ctx, "uniswapv3", duration, true)
	}

	// Log with clear interpretation
	feeMultiplier := float64(feeTier) / 1000000.0
	if isToken0In {
		// Buying base with quote
		u.logger.Info("fetched Uniswap price (QuoterV2)",
			"action", "buy_base_with_quote",
			"base_requested_raw", size.String(),
			"base_requested_normalized", amountOutBase.Text('f', 4),
			"quote_required_raw", firstAmount.String(),
			"quote_spent", amountOutQuote.Text('f', 2),
			"price", effectivePrice.Text('f', 2),
			"slippage_pct", slippagePct,
			"ticks_crossed", ticksCrossed,
			"gas_estimate", gasEstimate.String(),
			"gas_cost_wei", gasCost.String(),
			"duration_ms", duration.Milliseconds(),
		)
	} else {
		// Selling base for quote
		baseAmount := rawToFloat(size, u.token1Decimals)
		u.logger.Info("fetched Uniswap price (QuoterV2)",
			"action", "sell_base_for_quote",
			"base_input_raw", size.String(),
			"base_input_normalized", baseAmount.Text('f', 4),
			"quote_output_raw", firstAmount.String(),
			"quote_received", amountOutQuote.Text('f', 2),
			"price", effectivePrice.Text('f', 2),
			"slippage_pct", slippagePct,
			"ticks_crossed", ticksCrossed,
			"gas_estimate", gasEstimate.String(),
			"gas_cost_wei", gasCost.String(),
			"duration_ms", duration.Milliseconds(),
		)
	}

	// Convert amountOutQuote back to raw format for Price struct
	amountOutRawInt := new(big.Int)
	if isToken0In {
		// For buying base, firstAmount is already quote in raw format
		amountOutRawInt = firstAmount
	} else {
		// For selling base, firstAmount is already quote in raw format
		amountOutRawInt = firstAmount
	}

	price := &Price{
		Value:         effectivePrice,
		AmountOut:     amountOutQuote,
		AmountOutRaw:  amountOutRawInt,
		Slippage:      big.NewFloat(slippagePct), // Percent to match CEX slippage units
		GasCost:       gasCost,
		TradingFee:    big.NewFloat(feeMultiplier),
		FeeTier:       feeTier, // Track which fee tier was used
		BaseDecimals:  u.token1Decimals,
		QuoteDecimals: u.token0Decimals,
		Timestamp:     time.Now(),
	}

	// Cache with 15s TTL (~1.25 blocks)
	if u.cache != nil {
		ttl := 15 * time.Second
		if err := u.cache.Set(ctx, cacheKey, price, ttl); err != nil {
			span.NoticeError(err)
			u.logger.Warn("failed to cache DEX quote", "error", err)
		} else {
			u.logger.Debug("cached DEX quote",
				"block", blockNum,
				"fee_tier", feeTier,
				"ttl_seconds", 15,
			)
		}
	}

	setQuoteSpanAmounts(span, size, price, isToken0In)
	return price, nil
}

func setQuoteSpanAmounts(span observability.Span, size *big.Int, price *Price, isToken0In bool) {
	if span == nil || size == nil || price == nil || price.AmountOutRaw == nil {
		return
	}

	amountIn := size
	amountOut := price.AmountOutRaw
	if isToken0In {
		amountIn = price.AmountOutRaw
		amountOut = size
	}

	span.SetAttribute("amount_in", amountIn.String())
	span.SetAttribute("amount_out", amountOut.String())
}

// GetPoolState fetches the current state of the Uniswap V3 pool

// estimateGasCostWithPrice calculates gas cost using provided gas price
// No RPC call - uses size-based heuristics for gas units
func (u *UniswapProvider) estimateGasCostWithPrice(size *big.Int, gasPrice *big.Int) *big.Int {
	// Size buckets based on typical Uniswap V3 gas usage (base token decimals)
	sizeBase := rawToFloat(size, u.token1Decimals)
	sizeFloat, _ := sizeBase.Float64()

	var estimatedGas int64
	switch {
	case sizeFloat < 5: // Small trades (< 5 base units)
		estimatedGas = 125000
	case sizeFloat < 50: // Medium trades (5-50 base units)
		estimatedGas = 165000
	case sizeFloat < 200: // Large trades (50-200 base units)
		estimatedGas = 240000
	default: // Whale trades (> 200 base units)
		estimatedGas = 350000
	}

	// Calculate total cost: gasPrice * estimatedGas
	gasCost := new(big.Int).Mul(gasPrice, big.NewInt(estimatedGas))
	return gasCost
}

// EstimateGasCost estimates the gas cost for a swap
// DEPRECATED: Use GetPrice with gasPrice parameter instead
// This method makes an expensive eth_gasPrice RPC call
func (u *UniswapProvider) EstimateGasCost(ctx context.Context, size *big.Int, isToken0In bool) (*big.Int, error) {
	// Fetch current gas price (EXPENSIVE RPC CALL)
	gasPrice, err := u.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Use the new size-based estimation
	return u.estimateGasCostWithPrice(size, gasPrice), nil
}

// buildQuoteCacheKey builds a cache key for DEX quotes
// Key format: dex:quote:v1:{blockNum}:{token0}:{token1}:{size}:{direction}:{feeTier}
// Example: dex:quote:v1:12345:0xA0b86991:0xC02aaA39:1000000000000000000:buy:500
func (u *UniswapProvider) buildQuoteCacheKey(blockNum uint64, size *big.Int, isToken0In bool, feeTier uint32) string {
	var tokenIn, tokenOut, direction string

	if isToken0In {
		tokenIn = u.token0Address.Hex()[:10] // First 10 chars (0x + 8 hex)
		tokenOut = u.token1Address.Hex()[:10]
		direction = "buy"
	} else {
		tokenIn = u.token1Address.Hex()[:10]
		tokenOut = u.token0Address.Hex()[:10]
		direction = "sell"
	}

	return fmt.Sprintf("dex:quote:v1:%d:%s:%s:%s:%s:%d",
		blockNum, tokenIn, tokenOut, size.String(), direction, feeTier)
}

// Health returns the current health status of the Uniswap provider.
func (u *UniswapProvider) Health() ProviderHealth {
	u.healthMu.RLock()
	defer u.healthMu.RUnlock()
	h := u.health
	if u.cb != nil {
		h.CircuitState = u.cb.State().String()
	}
	return h
}

func (u *UniswapProvider) recordHealth(err error, duration time.Duration) {
	u.healthMu.Lock()
	defer u.healthMu.Unlock()

	u.health.LastDuration = duration
	if err == nil {
		u.health.LastSuccess = time.Now()
		u.health.LastError = ""
		u.health.ConsecutiveFailures = 0
		return
	}

	u.health.LastFailure = time.Now()
	u.health.LastError = err.Error()
	u.health.ConsecutiveFailures++
}

// Name returns the provider name for warmup logging.
func (u *UniswapProvider) Name() string {
	if u.pairName != "" {
		return fmt.Sprintf("uniswap:%s", u.pairName)
	}
	return "uniswap"
}

// Warmup pre-populates the cache with initial DEX quotes.
// This implements the cache.WarmupProvider interface.
// Uses a default trade size of 1 base token unit for warmup.
func (u *UniswapProvider) Warmup(ctx context.Context) error {
	// Default warmup size: 1 base token (e.g., 1 ETH)
	oneBaseUnit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(u.token1Decimals)), nil)
	return u.WarmupWithSizes(ctx, []*big.Int{oneBaseUnit})
}

// WarmupWithSizes pre-populates the cache with quotes for specific trade sizes.
func (u *UniswapProvider) WarmupWithSizes(ctx context.Context, sizes []*big.Int) error {
	// Get current block for cache key
	blockNum, err := u.client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get block number: %w", err)
	}

	// Default gas price for warmup (50 gwei)
	defaultGasPrice := big.NewInt(50e9)

	warmupCount := 0
	for _, size := range sizes {
		// Warm both directions for each size
		for _, isBuy := range []bool{true, false} {
			_, err := u.GetPrice(ctx, size, isBuy, defaultGasPrice, blockNum)
			if err != nil {
				u.logger.Warn("warmup quote failed",
					"size", size.String(),
					"is_buy", isBuy,
					"error", err,
				)
				// Continue with other sizes/directions
				continue
			}
			warmupCount++
		}
	}

	if warmupCount == 0 {
		return fmt.Errorf("all warmup quotes failed")
	}

	u.logger.Info("Uniswap cache warmed successfully",
		"pair", u.pairName,
		"quotes_cached", warmupCount,
		"sizes", len(sizes),
	)

	return nil
}
