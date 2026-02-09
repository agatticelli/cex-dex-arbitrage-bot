package pricing

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
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
	client         *ethclient.Client
	quoterContract *bind.BoundContract
	poolAddress    common.Address
	token0Address  common.Address // USDC
	token1Address  common.Address // WETH
	cache          cache.Cache
	logger         *observability.Logger
	metrics        *observability.Metrics
	feePips        int // Pool fee in pips (e.g., 3000 = 0.3%)
}

// UniswapProviderConfig holds Uniswap provider configuration
type UniswapProviderConfig struct {
	Client        *ethclient.Client
	QuoterAddress string // QuoterV2 contract address
	PoolAddress   string
	Token0Address string // USDC address
	Token1Address string // WETH address
	Cache         cache.Cache
	Logger        *observability.Logger
	Metrics       *observability.Metrics
	FeePips       int
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

	if cfg.FeePips == 0 {
		cfg.FeePips = 3000 // Default 0.3% fee
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
		client:         cfg.Client,
		quoterContract: quoterContract,
		poolAddress:    poolAddr,
		token0Address:  token0Addr,
		token1Address:  token1Addr,
		cache:          cfg.Cache,
		logger:         cfg.Logger,
		metrics:        cfg.Metrics,
		feePips:        cfg.FeePips,
	}, nil
}

// GetPrice fetches current price from Uniswap V3 pool by simulating a swap
// gasPrice is optional - if nil, will fetch from network (expensive)
// If provided (cached from detector), uses it directly (efficient)
func (u *UniswapProvider) GetPrice(ctx context.Context, size *big.Int, isToken0In bool, gasPrice *big.Int) (*Price, error) {
	start := time.Now()

	// PRODUCTION-READY: Use QuoterV2 for accurate price quotes
	// QuoterV2 handles all the complex Uniswap V3 math internally:
	// - Tick-walking across multiple price ranges
	// - Exact liquidity calculations at each tick
	// - Proper handling of fees and slippage
	// - Gas estimation for the actual swap

	// Determine token addresses and call appropriate QuoterV2 method
	// Note: The `size` parameter is always in ETH wei for both directions
	var tokenIn, tokenOut common.Address
	var result []interface{}
	callOpts := &bind.CallOpts{Context: ctx}

	if isToken0In {
		// Buying ETH with USDC: USDC (token0) -> ETH (token1)
		// size is the ETH amount we want to buy (in wei)
		// Use quoteExactOutputSingle to get required USDC for exact ETH output

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
			Amount:            size, // Desired ETH output
			Fee:               big.NewInt(int64(u.feePips)),
			SqrtPriceLimitX96: big.NewInt(0),
		}

		err := u.quoterContract.Call(callOpts, &result, "quoteExactOutputSingle", params)
		if err != nil {
			return nil, fmt.Errorf("QuoterV2 quoteExactOutputSingle failed: %w", err)
		}
	} else {
		// Selling ETH for USDC: ETH (token1) -> USDC (token0)
		// size is the ETH amount we want to sell (in wei)
		// Use quoteExactInputSingle to get USDC output for exact ETH input

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
			AmountIn:          size, // ETH input
			Fee:               big.NewInt(int64(u.feePips)),
			SqrtPriceLimitX96: big.NewInt(0),
		}

		err := u.quoterContract.Call(callOpts, &result, "quoteExactInputSingle", params)
		if err != nil {
			return nil, fmt.Errorf("QuoterV2 quoteExactInputSingle failed: %w", err)
		}
	}

	// Parse results - format differs between quoteExactInputSingle and quoteExactOutputSingle
	// Both return: (amount, sqrtPriceX96After, initializedTicksCrossed, gasEstimate)
	firstAmount := result[0].(*big.Int)
	// sqrtPriceAfter := result[1].(*big.Int) // Not used for now
	ticksCrossed := result[2].(uint32)
	gasEstimate := result[3].(*big.Int)

	var amountOutUSDC *big.Float
	var effectivePrice *big.Float
	var amountOutETH *big.Float // For logging

	if isToken0In {
		// Buying ETH with USDC: used quoteExactOutputSingle
		// firstAmount is the USDC input required
		// size is the ETH output (what we requested)

		usdcSpent := new(big.Float).SetInt(firstAmount)
		usdcSpent.Quo(usdcSpent, big.NewFloat(1e6))

		amountOutETH = new(big.Float).SetInt(size)
		amountOutETH.Quo(amountOutETH, big.NewFloat(1e18))

		// Price = USDC spent / ETH received
		effectivePrice = new(big.Float).Quo(usdcSpent, amountOutETH)
		amountOutUSDC = usdcSpent
	} else {
		// Selling ETH for USDC: used quoteExactInputSingle
		// firstAmount is the USDC output received
		// size is the ETH input

		amountOutUSDC = new(big.Float).SetInt(firstAmount)
		amountOutUSDC.Quo(amountOutUSDC, big.NewFloat(1e6))

		ethAmount := new(big.Float).SetInt(size)
		ethAmount.Quo(ethAmount, big.NewFloat(1e18))

		effectivePrice = new(big.Float).Quo(amountOutUSDC, ethAmount)
	}

	// Calculate slippage from sqrtPriceAfter (price impact percentage)
	// This requires getting current price first - for now use ticks crossed as proxy
	slippagePct := float64(ticksCrossed) * 0.01 // Rough estimate: 0.01% per tick

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
	feeMultiplier := float64(u.feePips) / 1000000.0
	if isToken0In {
		// Buying ETH with USDC
		u.logger.Info("fetched Uniswap price (QuoterV2)",
			"action", "buy_eth_with_usdc",
			"eth_requested_wei", size.String(),
			"eth_requested_normalized", amountOutETH.Text('f', 4),
			"usdc_required_raw", firstAmount.String(),
			"usdc_spent", amountOutUSDC.Text('f', 2),
			"price", effectivePrice.Text('f', 2),
			"slippage_pct", slippagePct,
			"ticks_crossed", ticksCrossed,
			"gas_estimate", gasEstimate.String(),
			"gas_cost_wei", gasCost.String(),
			"duration_ms", duration.Milliseconds(),
		)
	} else {
		// Selling ETH for USDC
		ethAmount := new(big.Float).SetInt(size)
		ethAmount.Quo(ethAmount, big.NewFloat(1e18))
		u.logger.Info("fetched Uniswap price (QuoterV2)",
			"action", "sell_eth_for_usdc",
			"eth_input_wei", size.String(),
			"eth_input_normalized", ethAmount.Text('f', 4),
			"usdc_output_raw", firstAmount.String(),
			"usdc_received", amountOutUSDC.Text('f', 2),
			"price", effectivePrice.Text('f', 2),
			"slippage_pct", slippagePct,
			"ticks_crossed", ticksCrossed,
			"gas_estimate", gasEstimate.String(),
			"gas_cost_wei", gasCost.String(),
			"duration_ms", duration.Milliseconds(),
		)
	}

	// Convert amountOutUSDC back to raw format for Price struct
	amountOutRawInt := new(big.Int)
	if isToken0In {
		// For buying ETH, firstAmount is already USDC in raw format
		amountOutRawInt = firstAmount
	} else {
		// For selling ETH, firstAmount is already USDC in raw format
		amountOutRawInt = firstAmount
	}

	return &Price{
		Value:        effectivePrice,
		AmountOut:    amountOutUSDC,
		AmountOutRaw: amountOutRawInt,
		Slippage:     big.NewFloat(slippagePct / 100), // Convert to decimal
		GasCost:      gasCost,
		TradingFee:   big.NewFloat(feeMultiplier),
		Timestamp:    time.Now(),
	}, nil
}

// GetPoolState fetches the current state of the Uniswap V3 pool

// estimateGasCostWithPrice calculates gas cost using provided gas price
// No RPC call - uses size-based heuristics for gas units
func (u *UniswapProvider) estimateGasCostWithPrice(size *big.Int, gasPrice *big.Int) *big.Int {
	// Size buckets based on typical Uniswap V3 gas usage
	sizeETH := new(big.Float).Quo(new(big.Float).SetInt(size), big.NewFloat(1e18))
	sizeFloat, _ := sizeETH.Float64()

	var estimatedGas int64
	switch {
	case sizeFloat < 5: // Small trades (< 5 ETH)
		estimatedGas = 125000
	case sizeFloat < 50: // Medium trades (5-50 ETH)
		estimatedGas = 165000
	case sizeFloat < 200: // Large trades (50-200 ETH)
		estimatedGas = 240000
	default: // Whale trades (> 200 ETH)
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
