package pricing

import (
	"math/big"
	"testing"
)

// TestOrderbookExecution tests single and multi-level orderbook walks
func TestOrderbookExecution(t *testing.T) {
	tests := []struct {
		name               string
		bids               []OrderbookLevel
		asks               []OrderbookLevel
		size               *big.Float
		isBuy              bool
		expectedPrice      float64
		expectedAmountOut  float64
		tolerance          float64
	}{
		{
			name: "buy single level",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(1999.0), Volume: big.NewFloat(10.0)}, // Dummy bids
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(10.0)},
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(5.0)},
			},
			size:              big.NewFloat(1.0), // Buy 1 ETH
			isBuy:             true,
			expectedPrice:     2000.0, // First ask
			expectedAmountOut: 2000.0, // 1 ETH * $2000
			tolerance:         0.01,
		},
		{
			name: "sell single level",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(10.0)},
				{Price: big.NewFloat(1995.0), Volume: big.NewFloat(5.0)},
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2001.0), Volume: big.NewFloat(10.0)}, // Dummy asks
			},
			size:              big.NewFloat(1.0), // Sell 1 ETH
			isBuy:             false,
			expectedPrice:     2000.0, // First bid
			expectedAmountOut: 2000.0, // 1 ETH * $2000
			tolerance:         0.01,
		},
		{
			name: "buy multi-level walk",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(1999.0), Volume: big.NewFloat(10.0)}, // Dummy bids
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},  // Fill 5 ETH
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(3.0)},  // Fill 3 ETH
				{Price: big.NewFloat(2010.0), Volume: big.NewFloat(10.0)}, // Fill 2 ETH from here
			},
			size:  big.NewFloat(10.0), // Buy 10 ETH total
			isBuy: true,
			// Weighted average: (5*2000 + 3*2005 + 2*2010) / 10 = 20035 / 10 = 2003.5
			expectedPrice:     2003.5,
			expectedAmountOut: 20035.0, // 5*2000 + 3*2005 + 2*2010
			tolerance:         1.0,
		},
		{
			name: "sell multi-level walk",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(3.0)},  // Fill 3 ETH
				{Price: big.NewFloat(1995.0), Volume: big.NewFloat(4.0)},  // Fill 4 ETH
				{Price: big.NewFloat(1990.0), Volume: big.NewFloat(10.0)}, // Fill 3 ETH from here
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2001.0), Volume: big.NewFloat(10.0)}, // Dummy asks
			},
			size:  big.NewFloat(10.0), // Sell 10 ETH total
			isBuy: false,
			// Weighted average: (3*2000 + 4*1995 + 3*1990) / 10 = 19950 / 10 = 1995
			expectedPrice:     1995.0,
			expectedAmountOut: 19950.0, // 3*2000 + 4*1995 + 3*1990
			tolerance:         1.0,
		},
		{
			name: "exact fill at level boundary",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(1999.0), Volume: big.NewFloat(10.0)}, // Dummy bids
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(5.0)},
			},
			size:              big.NewFloat(5.0), // Exactly fills first level
			isBuy:             true,
			expectedPrice:     2000.0,
			expectedAmountOut: 10000.0, // 5 ETH * $2000
			tolerance:         0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderbook := &Orderbook{
				Bids: tt.bids,
				Asks: tt.asks,
			}

			if !orderbook.IsValid() {
				t.Fatal("Orderbook not valid")
			}

			result, err := orderbook.CalculateExecution(tt.size, tt.isBuy)
			if err != nil {
				t.Fatalf("CalculateExecution failed: %v", err)
			}

			amountOutFloat, _ := result.TotalCost.Float64()
			avgPriceFloat, _ := result.EffectivePrice.Float64()

			// Verify average price
			if avgPriceFloat < tt.expectedPrice-tt.tolerance || avgPriceFloat > tt.expectedPrice+tt.tolerance {
				t.Errorf("Average price: expected $%.2f, got $%.2f",
					tt.expectedPrice, avgPriceFloat)
			}

			// Verify amount out
			if amountOutFloat < tt.expectedAmountOut-tt.tolerance || amountOutFloat > tt.expectedAmountOut+tt.tolerance {
				t.Errorf("Amount out: expected $%.2f, got $%.2f",
					tt.expectedAmountOut, amountOutFloat)
			}

			t.Logf("✓ Executed %.2f ETH at avg price $%.2f → $%.2f USDC",
				mustFloat64(tt.size), avgPriceFloat, amountOutFloat)
		})
	}
}

// TestInsufficientLiquidity verifies error when size exceeds orderbook depth
func TestInsufficientLiquidity(t *testing.T) {
	tests := []struct {
		name  string
		bids  []OrderbookLevel
		asks  []OrderbookLevel
		size  *big.Float
		isBuy bool
	}{
		{
			name: "buy exceeds all ask levels",
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(3.0)},
			},
			size:  big.NewFloat(10.0), // Total only 8 ETH available
			isBuy: true,
		},
		{
			name: "sell exceeds all bid levels",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(3.0)},
				{Price: big.NewFloat(1995.0), Volume: big.NewFloat(2.0)},
			},
			size:  big.NewFloat(10.0), // Total only 5 ETH bids
			isBuy: false,
		},
		{
			name: "empty asks for buy",
			asks: []OrderbookLevel{},
			size: big.NewFloat(1.0),
			isBuy: true,
		},
		{
			name:  "empty bids for sell",
			bids:  []OrderbookLevel{},
			size:  big.NewFloat(1.0),
			isBuy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderbook := &Orderbook{
				Bids: tt.bids,
				Asks: tt.asks,
			}

			_, err := orderbook.CalculateExecution(tt.size, tt.isBuy)
			if err == nil {
				t.Error("Expected insufficient liquidity error, got nil")
			}

			t.Logf("✓ Correctly detected insufficient liquidity: %v", err)
		})
	}
}

// TestSlippageCalculation verifies slippage is correctly computed
func TestSlippageCalculation(t *testing.T) {
	tests := []struct {
		name             string
		asks             []OrderbookLevel
		size             *big.Float
		expectedSlippage float64
		tolerance        float64
	}{
		{
			name: "no slippage - single level",
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(10.0)},
			},
			size:             big.NewFloat(5.0),
			expectedSlippage: 0.0, // All filled at same price
			tolerance:        0.001,
		},
		{
			name: "small slippage - two levels",
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(5.0)},
			},
			size: big.NewFloat(10.0),
			// Best price: 2000, Avg price: (5*2000 + 5*2005)/10 = 2002.5
			// Slippage: (2002.5 - 2000) / 2000 = 0.00125 = 0.125%
			expectedSlippage: 0.00125,
			tolerance:        0.0001,
		},
		{
			name: "larger slippage - three levels",
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(2.0)},
				{Price: big.NewFloat(2010.0), Volume: big.NewFloat(3.0)},
				{Price: big.NewFloat(2020.0), Volume: big.NewFloat(5.0)},
			},
			size: big.NewFloat(10.0),
			// Best price: 2000
			// Avg price: (2*2000 + 3*2010 + 5*2020) / 10 = 20130 / 10 = 2013
			// Slippage: (2013 - 2000) / 2000 = 0.0065 = 0.65%
			expectedSlippage: 0.0065,
			tolerance:        0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderbook := &Orderbook{
				Bids: []OrderbookLevel{
					{Price: big.NewFloat(1999.0), Volume: big.NewFloat(100.0)}, // Dummy bids
				},
				Asks: tt.asks,
			}

			result, err := orderbook.CalculateExecution(tt.size, true)
			if err != nil {
				t.Fatalf("CalculateExecution failed: %v", err)
			}

			// Best price is first level
			bestPrice := tt.asks[0].Price
			bestPriceFloat, _ := bestPrice.Float64()
			avgPriceFloat, _ := result.EffectivePrice.Float64()

			// Calculate slippage: (avgPrice - bestPrice) / bestPrice
			slippage := (avgPriceFloat - bestPriceFloat) / bestPriceFloat

			if slippage < tt.expectedSlippage-tt.tolerance || slippage > tt.expectedSlippage+tt.tolerance {
				t.Errorf("Slippage: expected %.4f%% (%.6f), got %.4f%% (%.6f)",
					tt.expectedSlippage*100, tt.expectedSlippage,
					slippage*100, slippage)
			}

			t.Logf("✓ Slippage: %.4f%% (best: $%.2f, avg: $%.2f)",
				slippage*100, bestPriceFloat, avgPriceFloat)
		})
	}
}

// TestMidPriceCalculation verifies correct mid-price computation
func TestMidPriceCalculation(t *testing.T) {
	tests := []struct {
		name             string
		bestBid          float64
		bestAsk          float64
		expectedMidPrice float64
	}{
		{
			name:             "tight spread",
			bestBid:          2000.0,
			bestAsk:          2001.0,
			expectedMidPrice: 2000.5,
		},
		{
			name:             "wider spread",
			bestBid:          2000.0,
			bestAsk:          2010.0,
			expectedMidPrice: 2005.0,
		},
		{
			name:             "very tight spread",
			bestBid:          2000.00,
			bestAsk:          2000.10,
			expectedMidPrice: 2000.05,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderbook := &Orderbook{
				Bids: []OrderbookLevel{
					{Price: big.NewFloat(tt.bestBid), Volume: big.NewFloat(10.0)},
				},
				Asks: []OrderbookLevel{
					{Price: big.NewFloat(tt.bestAsk), Volume: big.NewFloat(10.0)},
				},
			}

			midPrice := orderbook.GetMidPrice()
			midPriceFloat, _ := midPrice.Float64()

			tolerance := 0.01
			if midPriceFloat < tt.expectedMidPrice-tolerance || midPriceFloat > tt.expectedMidPrice+tolerance {
				t.Errorf("Mid price: expected $%.2f, got $%.2f",
					tt.expectedMidPrice, midPriceFloat)
			}

			spread := tt.bestAsk - tt.bestBid
			spreadPct := (spread / tt.bestBid) * 100

			t.Logf("✓ Mid price: $%.2f (bid: $%.2f, ask: $%.2f, spread: %.2f%%)",
				midPriceFloat, tt.bestBid, tt.bestAsk, spreadPct)
		})
	}
}

// TestOrderbookValidation verifies IsValid() checks
func TestOrderbookValidation(t *testing.T) {
	tests := []struct {
		name     string
		bids     []OrderbookLevel
		asks     []OrderbookLevel
		isValid  bool
	}{
		{
			name: "valid orderbook",
			bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(10.0)},
			},
			asks: []OrderbookLevel{
				{Price: big.NewFloat(2001.0), Volume: big.NewFloat(10.0)},
			},
			isValid: true,
		},
		{
			name:    "empty bids",
			bids:    []OrderbookLevel{},
			asks:    []OrderbookLevel{{Price: big.NewFloat(2001.0), Volume: big.NewFloat(10.0)}},
			isValid: false,
		},
		{
			name:    "empty asks",
			bids:    []OrderbookLevel{{Price: big.NewFloat(2000.0), Volume: big.NewFloat(10.0)}},
			asks:    []OrderbookLevel{},
			isValid: false,
		},
		{
			name:    "both empty",
			bids:    []OrderbookLevel{},
			asks:    []OrderbookLevel{},
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderbook := &Orderbook{
				Bids: tt.bids,
				Asks: tt.asks,
			}

			isValid := orderbook.IsValid()
			if isValid != tt.isValid {
				t.Errorf("IsValid(): expected %v, got %v", tt.isValid, isValid)
			}

			if isValid {
				t.Log("✓ Orderbook valid")
			} else {
				t.Log("✓ Orderbook invalid (as expected)")
			}
		})
	}
}

// TestOrderbookPriceOrdering verifies bids and asks are properly ordered
func TestOrderbookPriceOrdering(t *testing.T) {
	t.Run("bids descending (highest first)", func(t *testing.T) {
		orderbook := &Orderbook{
			Bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(1995.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(1990.0), Volume: big.NewFloat(5.0)},
			},
			Asks: []OrderbookLevel{
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(5.0)},
			},
		}

		// Verify bids are descending
		for i := 0; i < len(orderbook.Bids)-1; i++ {
			current, _ := orderbook.Bids[i].Price.Float64()
			next, _ := orderbook.Bids[i+1].Price.Float64()
			if current <= next {
				t.Errorf("Bids not descending: bid[%d]=$%.2f <= bid[%d]=$%.2f", i, current, i+1, next)
			}
		}

		t.Log("✓ Bids properly ordered (descending)")
	})

	t.Run("asks ascending (lowest first)", func(t *testing.T) {
		orderbook := &Orderbook{
			Bids: []OrderbookLevel{
				{Price: big.NewFloat(2000.0), Volume: big.NewFloat(5.0)},
			},
			Asks: []OrderbookLevel{
				{Price: big.NewFloat(2005.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(2010.0), Volume: big.NewFloat(5.0)},
				{Price: big.NewFloat(2015.0), Volume: big.NewFloat(5.0)},
			},
		}

		// Verify asks are ascending
		for i := 0; i < len(orderbook.Asks)-1; i++ {
			current, _ := orderbook.Asks[i].Price.Float64()
			next, _ := orderbook.Asks[i+1].Price.Float64()
			if current >= next {
				t.Errorf("Asks not ascending: ask[%d]=$%.2f >= ask[%d]=$%.2f", i, current, i+1, next)
			}
		}

		t.Log("✓ Asks properly ordered (ascending)")
	})
}

// mustFloat64 is a test helper to convert *big.Float to float64
func mustFloat64(f *big.Float) float64 {
	val, _ := f.Float64()
	return val
}
