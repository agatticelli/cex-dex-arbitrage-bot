package config

import (
	"testing"
)

// TestParsePair_ValidETHPairs tests parsing of valid ETH-X pairs
func TestParsePair_ValidETHPairs(t *testing.T) {
	tests := []struct {
		name         string
		pairName     string
		expectedBase string
		expectedQuote string
	}{
		{
			name:         "ETH-USDC",
			pairName:     "ETH-USDC",
			expectedBase: "ETH",
			expectedQuote: "USDC",
		},
		{
			name:         "ETH-USDT",
			pairName:     "ETH-USDT",
			expectedBase: "ETH",
			expectedQuote: "USDT",
		},
		{
			name:         "ETH-DAI",
			pairName:     "ETH-DAI",
			expectedBase: "ETH",
			expectedQuote: "DAI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, quote, err := ParsePair(tt.pairName)
			if err != nil {
				t.Fatalf("ParsePair(%s) failed: %v", tt.pairName, err)
			}

			if base.Symbol != tt.expectedBase {
				t.Errorf("Base symbol: expected %s, got %s", tt.expectedBase, base.Symbol)
			}

			if quote.Symbol != tt.expectedQuote {
				t.Errorf("Quote symbol: expected %s, got %s", tt.expectedQuote, quote.Symbol)
			}

			// Verify base is ETH with correct address
			if base.Symbol == "ETH" {
				expectedAddress := "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
				if base.Address != expectedAddress {
					t.Errorf("ETH address: expected %s, got %s", expectedAddress, base.Address)
				}
				if base.Decimals != 18 {
					t.Errorf("ETH decimals: expected 18, got %d", base.Decimals)
				}
			}

			t.Logf("✓ Parsed %s: base=%s (%s), quote=%s (%s)",
				tt.pairName, base.Symbol, base.Address[:10], quote.Symbol, quote.Address[:10])
		})
	}
}

// TestParsePair_ValidNonETHPairs tests parsing of valid non-ETH base pairs
func TestParsePair_ValidNonETHPairs(t *testing.T) {
	tests := []struct {
		name          string
		pairName      string
		expectedBase  string
		expectedQuote string
	}{
		{
			name:          "WBTC-USDC",
			pairName:      "WBTC-USDC",
			expectedBase:  "WBTC",
			expectedQuote: "USDC",
		},
		{
			name:          "LINK-USDT",
			pairName:      "LINK-USDT",
			expectedBase:  "LINK",
			expectedQuote: "USDT",
		},
		{
			name:          "UNI-DAI",
			pairName:      "UNI-DAI",
			expectedBase:  "UNI",
			expectedQuote: "DAI",
		},
		{
			name:          "AAVE-USDC",
			pairName:      "AAVE-USDC",
			expectedBase:  "AAVE",
			expectedQuote: "USDC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, quote, err := ParsePair(tt.pairName)
			if err != nil {
				t.Fatalf("ParsePair(%s) failed: %v", tt.pairName, err)
			}

			if base.Symbol != tt.expectedBase {
				t.Errorf("Base symbol: expected %s, got %s", tt.expectedBase, base.Symbol)
			}

			if quote.Symbol != tt.expectedQuote {
				t.Errorf("Quote symbol: expected %s, got %s", tt.expectedQuote, quote.Symbol)
			}

			t.Logf("✓ Parsed %s: base=%s, quote=%s", tt.pairName, base.Symbol, quote.Symbol)
		})
	}
}

// TestParsePair_InvalidPairs tests rejection of invalid pairs
func TestParsePair_InvalidPairs(t *testing.T) {
	tests := []struct {
		name     string
		pairName string
		errorMsg string
	}{
		{
			name:     "BTC-USDC (not in registry)",
			pairName: "BTC-USDC",
			errorMsg: "unknown base token",
		},
		{
			name:     "ETH-BTC (invalid quote token)",
			pairName: "ETH-BTC",
			errorMsg: "unknown quote token",
		},
		{
			name:     "ETH-ETH (same token)",
			pairName: "ETH-ETH",
			errorMsg: "must be different",
		},
		{
			name:     "invalid format",
			pairName: "ETHUSDC",
			errorMsg: "invalid pair format",
		},
		{
			name:     "too many dashes",
			pairName: "ETH-USDC-DAI",
			errorMsg: "invalid pair format",
		},
		{
			name:     "UNKNOWN-USDC",
			pairName: "UNKNOWN-USDC",
			errorMsg: "unknown base token",
		},
		{
			name:     "ETH-UNKNOWN",
			pairName: "ETH-UNKNOWN",
			errorMsg: "unknown quote token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParsePair(tt.pairName)
			if err == nil {
				t.Errorf("ParsePair(%s) should have failed but succeeded", tt.pairName)
				return
			}

			// Check error message contains expected substring
			if !contains(err.Error(), tt.errorMsg) {
				t.Errorf("Error message should contain '%s', got: %s", tt.errorMsg, err.Error())
			}

			t.Logf("✓ Correctly rejected %s: %v", tt.pairName, err)
		})
	}
}

// TestFormatCEXSymbol tests CEX symbol formatting
func TestFormatCEXSymbol(t *testing.T) {
	tests := []struct {
		pairName string
		expected string
	}{
		{"ETH-USDC", "ETHUSDC"},
		{"ETH-USDT", "ETHUSDT"},
		{"ETH-DAI", "ETHDAI"},
	}

	for _, tt := range tests {
		t.Run(tt.pairName, func(t *testing.T) {
			result := FormatCEXSymbol(tt.pairName)
			if result != tt.expected {
				t.Errorf("FormatCEXSymbol(%s): expected %s, got %s", tt.pairName, tt.expected, result)
			}

			t.Logf("✓ %s → %s", tt.pairName, result)
		})
	}
}

// TestTokenRegistry_ExpectedTokens verifies registry contains all expected tokens
func TestTokenRegistry_ExpectedTokens(t *testing.T) {
	expectedTokens := map[string]bool{
		// Base tokens
		"ETH":  true,
		"WBTC": true,
		"LINK": true,
		"UNI":  true,
		"AAVE": true,
		// Quote tokens (stablecoins)
		"USDC": true,
		"USDT": true,
		"DAI":  true,
	}

	for symbol := range TokenRegistry {
		if !expectedTokens[symbol] {
			t.Errorf("Unexpected token in registry: %s", symbol)
		}
	}

	for symbol := range expectedTokens {
		if _, exists := TokenRegistry[symbol]; !exists {
			t.Errorf("Expected token missing from registry: %s", symbol)
		}
	}

	t.Logf("✓ Token registry contains: %v", getKeys(TokenRegistry))
}

// TestTokenRegistry_ETHMetadata verifies ETH token metadata
func TestTokenRegistry_ETHMetadata(t *testing.T) {
	eth, exists := TokenRegistry["ETH"]
	if !exists {
		t.Fatal("ETH not found in TokenRegistry")
	}

	if eth.Symbol != "ETH" {
		t.Errorf("ETH Symbol: expected 'ETH', got %s", eth.Symbol)
	}

	expectedAddress := "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	if eth.Address != expectedAddress {
		t.Errorf("ETH Address: expected %s, got %s", expectedAddress, eth.Address)
	}

	if eth.Decimals != 18 {
		t.Errorf("ETH Decimals: expected 18, got %d", eth.Decimals)
	}

	if eth.IsStablecoin {
		t.Error("ETH should not be marked as stablecoin")
	}

	t.Logf("✓ ETH metadata correct: %s (%d decimals)", eth.Address, eth.Decimals)
}

// TestTokenRegistry_StablecoinsMarked verifies stablecoins are correctly marked
func TestTokenRegistry_StablecoinsMarked(t *testing.T) {
	stablecoins := []string{"USDC", "USDT", "DAI"}

	for _, symbol := range stablecoins {
		token, exists := TokenRegistry[symbol]
		if !exists {
			t.Errorf("%s not found in TokenRegistry", symbol)
			continue
		}

		if !token.IsStablecoin {
			t.Errorf("%s should be marked as stablecoin", symbol)
		}

		if token.Decimals != 6 && token.Decimals != 18 {
			t.Errorf("%s decimals should be 6 or 18, got %d", symbol, token.Decimals)
		}

		t.Logf("✓ %s marked as stablecoin (%d decimals)", symbol, token.Decimals)
	}
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func getKeys(m map[string]TokenInfo) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
