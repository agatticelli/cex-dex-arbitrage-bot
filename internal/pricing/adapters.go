package pricing

import (
	"context"
	"math/big"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
)

// init registers pricing types with the cache for proper Redis deserialization.
// This ensures that when *Orderbook or *Price is stored in Redis and retrieved,
// it will be deserialized to the correct Go type instead of map[string]interface{}.
func init() {
	cache.RegisterCacheType((*Orderbook)(nil))
	cache.RegisterCacheType((*Price)(nil))
}

// BinanceAdapter adapts BinanceProvider to the arbitrage.PriceProvider interface.
// It wraps a BinanceProvider with symbol-specific metadata for multi-pair support.
type BinanceAdapter struct {
	Provider      *BinanceProvider
	Symbol        string
	BaseSymbol    string
	QuoteSymbol   string
	BaseDecimals  int
	QuoteDecimals int
	QuoteIsStable bool
}

// GetPrice fetches the price from Binance for the configured symbol.
// Gas price and block number are ignored for CEX providers.
func (b *BinanceAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*Price, error) {
	// CEX doesn't use gas price, but we scope orderbook snapshot cache per block.
	price, err := b.Provider.GetPriceForBlock(ctx, b.Symbol, size, isBuy, b.BaseDecimals, b.QuoteDecimals, blockNum)
	if err != nil {
		return nil, err
	}

	// Attach metadata for downstream calculations
	price.BaseSymbol = b.BaseSymbol
	price.QuoteSymbol = b.QuoteSymbol
	price.BaseDecimals = b.BaseDecimals
	price.QuoteDecimals = b.QuoteDecimals
	price.QuoteIsStable = b.QuoteIsStable

	return price, nil
}

// GetETHPrice fetches the current ETH price in USD from Binance.
func (b *BinanceAdapter) GetETHPrice(ctx context.Context) (float64, error) {
	return b.Provider.GetETHPrice(ctx)
}

// UniswapAdapter adapts UniswapProvider to the arbitrage.PriceProvider interface.
// It wraps a UniswapProvider with symbol-specific metadata for multi-pair support.
type UniswapAdapter struct {
	Provider      *UniswapProvider
	BaseSymbol    string
	QuoteSymbol   string
	BaseDecimals  int
	QuoteDecimals int
	QuoteIsStable bool
}

// GetPrice fetches the price from Uniswap for the configured pool.
// Gas price is used for gas cost estimation, block number for caching.
func (u *UniswapAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*Price, error) {
	// Pass gas price and block number to DEX provider (for caching and gas estimation)
	price, err := u.Provider.GetPrice(ctx, size, isBuy, gasPrice, blockNum)
	if err != nil {
		return nil, err
	}

	// Attach metadata for downstream calculations
	price.BaseSymbol = u.BaseSymbol
	price.QuoteSymbol = u.QuoteSymbol
	price.BaseDecimals = u.BaseDecimals
	price.QuoteDecimals = u.QuoteDecimals
	price.QuoteIsStable = u.QuoteIsStable

	return price, nil
}
