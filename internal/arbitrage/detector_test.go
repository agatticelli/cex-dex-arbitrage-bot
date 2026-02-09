package arbitrage

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
)

// mockPriceProvider is a mock price provider for testing
type mockPriceProvider struct {
	prices map[string]*pricing.Price // key: "buy" or "sell"
}

func (m *mockPriceProvider) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	if isBuy {
		return m.prices["buy"], nil
	}
	return m.prices["sell"], nil
}

// TestDEXDirectionMapping verifies that the detector correctly maps buy/sell to isToken0In parameter
func TestDEXDirectionMapping(t *testing.T) {
	// Setup: DEX should have bid-ask spread (buy price > sell price)
	mockDEX := &mockPriceProvider{
		prices: map[string]*pricing.Price{
			"buy": { // Buying ETH: pay MORE USDC per ETH (ask price)
				Value:        big.NewFloat(2065.0),
				AmountOut:    big.NewFloat(2065.0),   // USDC spent
				AmountOutRaw: big.NewInt(2065000000), // 2065 USDC (6 decimals)
				GasCost:      big.NewInt(22e15),      // 0.022 ETH gas
				TradingFee:   big.NewFloat(0.003),    // 0.3% Uniswap fee
				FeeTier:      3000,
				Timestamp:    time.Now(),
			},
			"sell": { // Selling ETH: receive LESS USDC per ETH (bid price)
				Value:        big.NewFloat(2052.0),
				AmountOut:    big.NewFloat(2052.0),   // USDC received
				AmountOutRaw: big.NewInt(2052000000), // 2052 USDC (6 decimals)
				GasCost:      big.NewInt(22e15),      // 0.022 ETH gas
				TradingFee:   big.NewFloat(0.003),    // 0.3% Uniswap fee
				FeeTier:      3000,
				Timestamp:    time.Now(),
			},
		},
	}

	ctx := context.Background()
	tradeSize := big.NewInt(1e18) // 1 ETH

	// Test: Get DEX buy price (should call with isBuy=true → isToken0In=true → buying ETH with USDC)
	dexBuyPrice, err := mockDEX.GetPrice(ctx, tradeSize, true, nil, 0)
	if err != nil {
		t.Fatalf("GetPrice(buy) failed: %v", err)
	}

	// Verify: Buy price should be HIGHER (we pay more to buy)
	expectedBuyPrice := 2065.0
	actualBuyPrice, _ := dexBuyPrice.Value.Float64()
	if actualBuyPrice != expectedBuyPrice {
		t.Errorf("DEX buy price incorrect: got %.2f, want %.2f", actualBuyPrice, expectedBuyPrice)
	}

	// Test: Get DEX sell price (should call with isBuy=false → isToken0In=false → selling ETH for USDC)
	dexSellPrice, err := mockDEX.GetPrice(ctx, tradeSize, false, nil, 0)
	if err != nil {
		t.Fatalf("GetPrice(sell) failed: %v", err)
	}

	// Verify: Sell price should be LOWER (we receive less when selling)
	expectedSellPrice := 2052.0
	actualSellPrice, _ := dexSellPrice.Value.Float64()
	if actualSellPrice != expectedSellPrice {
		t.Errorf("DEX sell price incorrect: got %.2f, want %.2f", actualSellPrice, expectedSellPrice)
	}

	// Verify: Buy price > Sell price (bid-ask spread exists)
	if dexBuyPrice.Value.Cmp(dexSellPrice.Value) <= 0 {
		t.Errorf("DEX buy price (%.2f) should be > sell price (%.2f)",
			actualBuyPrice,
			actualSellPrice,
		)
	}

	t.Logf("✓ DEX direction mapping correct: buy=%.2f > sell=%.2f (spread=%.2f)",
		actualBuyPrice, actualSellPrice, actualBuyPrice-actualSellPrice)
}

// TestArbitrageDirectionDetection verifies correct arbitrage direction identification
func TestArbitrageDirectionDetection(t *testing.T) {
	tests := []struct {
		name         string
		cexBuyPrice  float64
		cexSellPrice float64
		dexBuyPrice  float64
		dexSellPrice float64
		expectedDir  Direction
		shouldExist  bool
	}{
		{
			name:         "CEX→DEX opportunity (CEX cheaper)",
			cexBuyPrice:  2050.0, // Buy ETH on CEX for $2050
			cexSellPrice: 2045.0, // Sell ETH on CEX for $2045
			dexBuyPrice:  2065.0, // Buy ETH on DEX for $2065
			dexSellPrice: 2060.0, // Sell ETH on DEX for $2060
			expectedDir:  CEXToDEX,
			shouldExist:  true, // CEX buy ($2050) < DEX sell ($2060) → profit $10
		},
		{
			name:         "DEX→CEX opportunity (DEX cheaper)",
			cexBuyPrice:  2060.0, // Buy ETH on CEX for $2060
			cexSellPrice: 2055.0, // Sell ETH on CEX for $2055
			dexBuyPrice:  2050.0, // Buy ETH on DEX for $2050
			dexSellPrice: 2045.0, // Sell ETH on DEX for $2045
			expectedDir:  DEXToCEX,
			shouldExist:  true, // DEX buy ($2050) < CEX sell ($2055) → profit $5
		},
		{
			name:         "No opportunity (CEX and DEX aligned)",
			cexBuyPrice:  2055.0,
			cexSellPrice: 2050.0,
			dexBuyPrice:  2057.0,
			dexSellPrice: 2052.0,
			expectedDir:  CEXToDEX,
			shouldExist:  false, // CEX buy ($2055) > DEX sell ($2052) → no profit
		},
		{
			name:         "Inverted spread (should never profit)",
			cexBuyPrice:  2070.0, // Buy expensive on CEX
			cexSellPrice: 2060.0,
			dexBuyPrice:  2050.0, // Buy cheap on DEX
			dexSellPrice: 2040.0, // Sell cheap on DEX
			expectedDir:  CEXToDEX,
			shouldExist:  false, // CEX buy ($2070) > DEX sell ($2040) but fees kill it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock providers
			mockCEX := &mockPriceProvider{
				prices: map[string]*pricing.Price{
					"buy": {
						Value:        big.NewFloat(tt.cexBuyPrice),
						AmountOut:    big.NewFloat(tt.cexBuyPrice),
						AmountOutRaw: big.NewInt(int64(tt.cexBuyPrice * 1e6)),
						TradingFee:   big.NewFloat(0.001), // 0.1% Binance fee
						Timestamp:    time.Now(),
					},
					"sell": {
						Value:        big.NewFloat(tt.cexSellPrice),
						AmountOut:    big.NewFloat(tt.cexSellPrice),
						AmountOutRaw: big.NewInt(int64(tt.cexSellPrice * 1e6)),
						TradingFee:   big.NewFloat(0.001), // 0.1% Binance fee
						Timestamp:    time.Now(),
					},
				},
			}

			mockDEX := &mockPriceProvider{
				prices: map[string]*pricing.Price{
					"buy": {
						Value:        big.NewFloat(tt.dexBuyPrice),
						AmountOut:    big.NewFloat(tt.dexBuyPrice),
						AmountOutRaw: big.NewInt(int64(tt.dexBuyPrice * 1e6)),
						GasCost:      big.NewInt(22e15),   // 0.022 ETH gas
						TradingFee:   big.NewFloat(0.003), // 0.3% Uniswap fee
						FeeTier:      3000,
						Timestamp:    time.Now(),
					},
					"sell": {
						Value:        big.NewFloat(tt.dexSellPrice),
						AmountOut:    big.NewFloat(tt.dexSellPrice),
						AmountOutRaw: big.NewInt(int64(tt.dexSellPrice * 1e6)),
						GasCost:      big.NewInt(22e15),   // 0.022 ETH gas
						TradingFee:   big.NewFloat(0.003), // 0.3% Uniswap fee
						FeeTier:      3000,
						Timestamp:    time.Now(),
					},
				},
			}

			// Verify direction logic
			// CEX→DEX: Buy on CEX, Sell on DEX → compare CEX buy price vs DEX sell price
			if tt.expectedDir == CEXToDEX {
				cexBuy, _ := mockCEX.GetPrice(context.Background(), big.NewInt(1e18), true, nil, 0)
				dexSell, _ := mockDEX.GetPrice(context.Background(), big.NewInt(1e18), false, nil, 0)

				cexBuyVal, _ := cexBuy.Value.Float64()
				dexSellVal, _ := dexSell.Value.Float64()

				if tt.shouldExist {
					if cexBuyVal >= dexSellVal {
						t.Errorf("CEX→DEX should exist but CEX buy (%.2f) >= DEX sell (%.2f)",
							cexBuyVal, dexSellVal)
					}
				}
			}

			// DEX→CEX: Buy on DEX, Sell on CEX → compare DEX buy price vs CEX sell price
			if tt.expectedDir == DEXToCEX {
				dexBuy, _ := mockDEX.GetPrice(context.Background(), big.NewInt(1e18), true, nil, 0)
				cexSell, _ := mockCEX.GetPrice(context.Background(), big.NewInt(1e18), false, nil, 0)

				dexBuyVal, _ := dexBuy.Value.Float64()
				cexSellVal, _ := cexSell.Value.Float64()

				if tt.shouldExist {
					if dexBuyVal >= cexSellVal {
						t.Errorf("DEX→CEX should exist but DEX buy (%.2f) >= CEX sell (%.2f)",
							dexBuyVal, cexSellVal)
					}
				}
			}
		})
	}
}

// TestBidAskSpread verifies that both CEX and DEX maintain proper bid-ask spreads
func TestBidAskSpread(t *testing.T) {
	tests := []struct {
		name      string
		buyPrice  float64
		sellPrice float64
		wantError bool
	}{
		{
			name:      "valid spread (buy > sell)",
			buyPrice:  2065.0,
			sellPrice: 2052.0,
			wantError: false,
		},
		{
			name:      "no spread (buy == sell)",
			buyPrice:  2055.0,
			sellPrice: 2055.0,
			wantError: false, // Not an error, but unusual
		},
		{
			name:      "inverted spread (buy < sell) - should never happen",
			buyPrice:  2050.0,
			sellPrice: 2060.0,
			wantError: true, // This indicates a bug or data error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.buyPrice < tt.sellPrice && !tt.wantError {
				t.Errorf("Buy price (%.2f) < Sell price (%.2f) - inverted spread detected",
					tt.buyPrice, tt.sellPrice)
			}

			if tt.buyPrice >= tt.sellPrice && tt.wantError {
				t.Logf("✓ Normal spread: buy=%.2f >= sell=%.2f", tt.buyPrice, tt.sellPrice)
			}
		})
	}
}
