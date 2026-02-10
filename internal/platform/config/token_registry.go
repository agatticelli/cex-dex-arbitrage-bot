package config

import (
	"fmt"
	"strings"
)

// TokenInfo contains token metadata for trading pairs
type TokenInfo struct {
	Symbol       string // Token symbol (ETH, BTC, USDC, etc.)
	Address      string // Ethereum mainnet address
	Decimals     int    // Token decimals (18 for ETH, 8 for BTC, 6 for USDC)
	IsStablecoin bool   // Whether this is a stablecoin
}

// TokenRegistry maps token symbols to their on-chain information
// This is a hardcoded registry of well-known tokens on Ethereum mainnet
var TokenRegistry = map[string]TokenInfo{
	"ETH": {
		Symbol:       "ETH",
		Address:      "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", // WETH
		Decimals:     18,
		IsStablecoin: false,
	},
	"WBTC": {
		Symbol:       "WBTC",
		Address:      "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599",
		Decimals:     8,
		IsStablecoin: false,
	},
	"LINK": {
		Symbol:       "LINK",
		Address:      "0x514910771AF9Ca656af840dff83E8264EcF986CA",
		Decimals:     18,
		IsStablecoin: false,
	},
	"UNI": {
		Symbol:       "UNI",
		Address:      "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984",
		Decimals:     18,
		IsStablecoin: false,
	},
	"AAVE": {
		Symbol:       "AAVE",
		Address:      "0x7Fc66500c84A76Ad7e9c93437bFc5Ac33E2DDaE9",
		Decimals:     18,
		IsStablecoin: false,
	},
	"USDC": {
		Symbol:       "USDC",
		Address:      "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		Decimals:     6,
		IsStablecoin: true,
	},
	"USDT": {
		Symbol:       "USDT",
		Address:      "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		Decimals:     6,
		IsStablecoin: true,
	},
	"DAI": {
		Symbol:       "DAI",
		Address:      "0x6B175474E89094C44Da98b954EedeAC495271d0F",
		Decimals:     18,
		IsStablecoin: true,
	},
}

// ParsePair parses a trading pair string like "ETH-USDC" into base and quote token info
// Returns the base token (left side), quote token (right side), and any error
//
// Supported base tokens: ETH, WBTC, LINK, UNI, AAVE
// Supported quote tokens: USDC, USDT, DAI
//
// Note: On Ethereum mainnet, gas is always paid in ETH regardless of trading pair.
// The system fetches ETH price separately for gas cost calculations.
//
// Example: ParsePair("ETH-USDC") returns:
//   - base:  TokenInfo{Symbol: "ETH", Address: "0xC02a...", Decimals: 18}
//   - quote: TokenInfo{Symbol: "USDC", Address: "0xA0b8...", Decimals: 6}
func ParsePair(pairName string) (base TokenInfo, quote TokenInfo, err error) {
	parts := strings.Split(pairName, "-")
	if len(parts) != 2 {
		return TokenInfo{}, TokenInfo{}, fmt.Errorf("invalid pair format: %s (expected BASE-QUOTE like ETH-USDC)", pairName)
	}

	baseSymbol, quoteSymbol := parts[0], parts[1]

	// Lookup base token
	base, ok := TokenRegistry[baseSymbol]
	if !ok {
		return TokenInfo{}, TokenInfo{}, fmt.Errorf("unknown base token: %s (supported: ETH, WBTC, LINK, UNI, AAVE)", baseSymbol)
	}

	// Lookup quote token
	quote, ok = TokenRegistry[quoteSymbol]
	if !ok {
		return TokenInfo{}, TokenInfo{}, fmt.Errorf("unknown quote token: %s (supported: USDC, USDT, DAI)", quoteSymbol)
	}

	// Validate that base and quote are different
	if baseSymbol == quoteSymbol {
		return TokenInfo{}, TokenInfo{}, fmt.Errorf("base and quote tokens must be different: %s", pairName)
	}

	return base, quote, nil
}

// FormatCEXSymbol converts a trading pair name to CEX symbol format
// Example: "ETH-USDC" → "ETHUSDC" (Binance format)
func FormatCEXSymbol(pairName string) string {
	return strings.ReplaceAll(pairName, "-", "")
}
