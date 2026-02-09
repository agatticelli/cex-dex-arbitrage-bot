package pricing

import (
	"fmt"
	"math/big"
	"time"
)

// Orderbook represents a market depth orderbook
type Orderbook struct {
	Bids      []OrderbookLevel // Buy orders (highest price first)
	Asks      []OrderbookLevel // Sell orders (lowest price first)
	Timestamp time.Time
}

// OrderbookLevel represents a single price level in the orderbook
type OrderbookLevel struct {
	Price  *big.Float // Price per unit
	Volume *big.Float // Volume available at this price
}

// ExecutionResult represents the result of simulating an orderbook execution
type ExecutionResult struct {
	EffectivePrice *big.Float // Weighted average price
	Slippage       *big.Float // Slippage percentage
	TotalCost      *big.Float // Total USDC (for buy: spent, for sell: received)
	TotalFilled    *big.Float // Total ETH (for buy: received, for sell: spent)
}

// CalculateExecution calculates the execution result for a given trade size
// accounting for slippage by walking through orderbook levels
// Returns actual USDC flows for accurate profit calculation
func (o *Orderbook) CalculateExecution(size *big.Float, isBuy bool) (*ExecutionResult, error) {
	if size.Cmp(big.NewFloat(0)) <= 0 {
		return nil, fmt.Errorf("invalid trade size: %v", size)
	}

	var levels []OrderbookLevel
	if isBuy {
		// For buy orders, we consume asks (sell orders)
		levels = o.Asks
	} else {
		// For sell orders, we consume bids (buy orders)
		levels = o.Bids
	}

	if len(levels) == 0 {
		return nil, fmt.Errorf("orderbook is empty")
	}

	// Walk through orderbook levels to calculate weighted average price
	remainingSize := new(big.Float).Copy(size)
	totalCost := big.NewFloat(0)
	totalFilled := big.NewFloat(0)

	for _, level := range levels {
		if remainingSize.Cmp(big.NewFloat(0)) <= 0 {
			break
		}

		// Determine how much we can fill at this level
		fillAmount := new(big.Float)
		if remainingSize.Cmp(level.Volume) <= 0 {
			// Can fill entire remaining size at this level
			fillAmount.Copy(remainingSize)
		} else {
			// Fill as much as available at this level
			fillAmount.Copy(level.Volume)
		}

		// Calculate cost at this level
		levelCost := new(big.Float).Mul(fillAmount, level.Price)
		totalCost.Add(totalCost, levelCost)
		totalFilled.Add(totalFilled, fillAmount)
		remainingSize.Sub(remainingSize, fillAmount)
	}

	// Check if we could fill the entire order
	if remainingSize.Cmp(big.NewFloat(0)) > 0 {
		return nil, fmt.Errorf("insufficient liquidity: could only fill %v of %v",
			totalFilled.Text('f', 4), size.Text('f', 4))
	}

	// Calculate weighted average price
	effectivePrice := new(big.Float).Quo(totalCost, totalFilled)

	// Calculate slippage percentage
	// Slippage = (effectivePrice - bestPrice) / bestPrice * 100
	bestPrice := levels[0].Price
	priceDiff := new(big.Float).Sub(effectivePrice, bestPrice)
	slippage := new(big.Float).Quo(priceDiff, bestPrice)
	slippage.Mul(slippage, big.NewFloat(100)) // Convert to percentage

	// For sell orders, slippage is negative (we get worse price)
	if !isBuy {
		slippage.Neg(slippage)
	}

	return &ExecutionResult{
		EffectivePrice: effectivePrice,
		Slippage:       slippage,
		TotalCost:      totalCost,      // USDC (buy: we pay, sell: we receive)
		TotalFilled:    totalFilled,    // ETH traded
	}, nil
}

// CalculateEffectivePrice calculates the effective execution price for a given trade size
// accounting for slippage by walking through orderbook levels
// DEPRECATED: Use CalculateExecution for accurate profit calculations
func (o *Orderbook) CalculateEffectivePrice(size *big.Float, isBuy bool) (*big.Float, *big.Float, error) {
	result, err := o.CalculateExecution(size, isBuy)
	if err != nil {
		return nil, nil, err
	}
	return result.EffectivePrice, result.Slippage, nil
}

// GetBestBid returns the highest bid price
func (o *Orderbook) GetBestBid() *big.Float {
	if len(o.Bids) == 0 {
		return big.NewFloat(0)
	}
	return o.Bids[0].Price
}

// GetBestAsk returns the lowest ask price
func (o *Orderbook) GetBestAsk() *big.Float {
	if len(o.Asks) == 0 {
		return big.NewFloat(0)
	}
	return o.Asks[0].Price
}

// GetSpread returns the bid-ask spread
func (o *Orderbook) GetSpread() *big.Float {
	if len(o.Bids) == 0 || len(o.Asks) == 0 {
		return big.NewFloat(0)
	}

	spread := new(big.Float).Sub(o.GetBestAsk(), o.GetBestBid())
	return spread
}

// GetSpreadPercentage returns the bid-ask spread as a percentage
func (o *Orderbook) GetSpreadPercentage() *big.Float {
	if len(o.Bids) == 0 || len(o.Asks) == 0 {
		return big.NewFloat(0)
	}

	spread := o.GetSpread()
	midPrice := new(big.Float).Add(o.GetBestBid(), o.GetBestAsk())
	midPrice.Quo(midPrice, big.NewFloat(2))

	spreadPct := new(big.Float).Quo(spread, midPrice)
	spreadPct.Mul(spreadPct, big.NewFloat(100))

	return spreadPct
}

// GetDepth calculates total liquidity within a price range
// priceRange: percentage from best price (e.g., 0.01 = 1%)
func (o *Orderbook) GetDepth(priceRange float64, isBuy bool) *big.Float {
	var levels []OrderbookLevel
	var bestPrice *big.Float

	if isBuy {
		levels = o.Asks
		bestPrice = o.GetBestAsk()
	} else {
		levels = o.Bids
		bestPrice = o.GetBestBid()
	}

	if len(levels) == 0 {
		return big.NewFloat(0)
	}

	// Calculate price threshold
	rangeMultiplier := big.NewFloat(1 + priceRange)
	if !isBuy {
		rangeMultiplier = big.NewFloat(1 - priceRange)
	}

	maxPrice := new(big.Float).Mul(bestPrice, rangeMultiplier)

	// Sum volume within range
	totalVolume := big.NewFloat(0)
	for _, level := range levels {
		withinRange := false
		if isBuy {
			// For buy orders, check if price <= maxPrice
			withinRange = level.Price.Cmp(maxPrice) <= 0
		} else {
			// For sell orders, check if price >= maxPrice
			withinRange = level.Price.Cmp(maxPrice) >= 0
		}

		if withinRange {
			totalVolume.Add(totalVolume, level.Volume)
		} else {
			break // Orderbook is sorted, so we can stop
		}
	}

	return totalVolume
}

// GetMidPrice returns the mid price (average of best bid and ask)
func (o *Orderbook) GetMidPrice() *big.Float {
	if len(o.Bids) == 0 || len(o.Asks) == 0 {
		return big.NewFloat(0)
	}

	midPrice := new(big.Float).Add(o.GetBestBid(), o.GetBestAsk())
	midPrice.Quo(midPrice, big.NewFloat(2))

	return midPrice
}

// IsValid checks if the orderbook is valid
func (o *Orderbook) IsValid() bool {
	return len(o.Bids) > 0 && len(o.Asks) > 0
}

// GetTotalBidVolume returns total volume across all bid levels
func (o *Orderbook) GetTotalBidVolume() *big.Float {
	total := big.NewFloat(0)
	for _, bid := range o.Bids {
		total.Add(total, bid.Volume)
	}
	return total
}

// GetTotalAskVolume returns total volume across all ask levels
func (o *Orderbook) GetTotalAskVolume() *big.Float {
	total := big.NewFloat(0)
	for _, ask := range o.Asks {
		total.Add(total, ask.Volume)
	}
	return total
}
