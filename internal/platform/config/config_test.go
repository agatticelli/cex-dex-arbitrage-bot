package config

import "testing"

func TestArbitrageConfig_Parse_TradeSizesBase_Default(t *testing.T) {
	cfg := ArbitrageConfig{
		Pairs:                     []string{"ETH-USDC"},
		DefaultTradeSizesBase:     []string{"1.5", "0.1"},
		DefaultMinProfitThreshold: 0.5,
	}

	if err := cfg.Parse(); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.parsedPairs) != 1 {
		t.Fatalf("expected 1 parsed pair, got %d", len(cfg.parsedPairs))
	}

	pair := cfg.parsedPairs[0]
	if len(pair.parsedTradeSizes) != 2 {
		t.Fatalf("expected 2 trade sizes, got %d", len(pair.parsedTradeSizes))
	}

	if got := pair.parsedTradeSizes[0].String(); got != "1500000000000000000" {
		t.Errorf("trade size base 1.5 ETH: expected 1500000000000000000, got %s", got)
	}
	if got := pair.parsedTradeSizes[1].String(); got != "100000000000000000" {
		t.Errorf("trade size base 0.1 ETH: expected 100000000000000000, got %s", got)
	}
}

func TestArbitrageConfig_Parse_TradeSizesBase_Override(t *testing.T) {
	cfg := ArbitrageConfig{
		Pairs:                     []string{"ETH-USDC", "ETH-USDT"},
		DefaultTradeSizes:         []string{"1000000000000000000"}, // 1 ETH raw
		DefaultMinProfitThreshold: 0.5,
		PairOverrides: map[string]PairOverride{
			"ETH-USDT": {
				TradeSizesBase: []string{"0.25"},
			},
		},
	}

	if err := cfg.Parse(); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.parsedPairs) != 2 {
		t.Fatalf("expected 2 parsed pairs, got %d", len(cfg.parsedPairs))
	}

	var overridePair *TradingPairConfig
	for i := range cfg.parsedPairs {
		if cfg.parsedPairs[i].Name == "ETH-USDT" {
			overridePair = &cfg.parsedPairs[i]
			break
		}
	}
	if overridePair == nil {
		t.Fatal("ETH-USDT pair not parsed")
	}

	if len(overridePair.parsedTradeSizes) != 1 {
		t.Fatalf("expected 1 override trade size, got %d", len(overridePair.parsedTradeSizes))
	}

	if got := overridePair.parsedTradeSizes[0].String(); got != "250000000000000000" {
		t.Errorf("trade size base 0.25 ETH: expected 250000000000000000, got %s", got)
	}
}
