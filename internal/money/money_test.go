package money

import (
	"testing"
)

func TestUSDArithmetic(t *testing.T) {
	tests := []struct {
		name     string
		a        USD
		b        USD
		op       string
		expected USD
	}{
		{"add positive", NewUSD(100.50), NewUSD(50.25), "add", NewUSD(150.75)},
		{"add zero", NewUSD(100), Zero(), "add", NewUSD(100)},
		{"sub positive", NewUSD(100.50), NewUSD(50.25), "sub", NewUSD(50.25)},
		{"sub to negative", NewUSD(50), NewUSD(100), "sub", NewUSD(-50)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result USD
			switch tt.op {
			case "add":
				result = tt.a.Add(tt.b)
			case "sub":
				result = tt.a.Sub(tt.b)
			}
			if result != tt.expected {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestUSDMulBPS(t *testing.T) {
	tests := []struct {
		name     string
		amount   USD
		bps      BPS
		expected USD
	}{
		{"1% of $100", NewUSD(100), NewBPS(1.0), NewUSD(1)},
		{"0.5% of $100", NewUSD(100), NewBPS(0.5), NewUSD(0.50)},
		{"0.1% of $1000", NewUSD(1000), NewBPS(0.1), NewUSD(1)},
		{"100% of $50", NewUSD(50), NewBPSFromInt(10000), NewUSD(50)},
		{"0% of $100", NewUSD(100), NewBPS(0), Zero()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.amount.MulBPS(tt.bps)
			if result != tt.expected {
				t.Errorf("got %v (%d cents), want %v (%d cents)",
					result, result.Cents(), tt.expected, tt.expected.Cents())
			}
		})
	}
}

func TestUSDString(t *testing.T) {
	tests := []struct {
		amount   USD
		expected string
	}{
		{NewUSD(100), "$100.00"},
		{NewUSD(0.50), "$0.50"},
		{NewUSD(-50.25), "-$50.25"},
		{Zero(), "$0.00"},
		{NewUSDFromCents(123456789), "$1234567.89"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.amount.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestCalculateSpreadBPS(t *testing.T) {
	tests := []struct {
		name     string
		cex      USD
		dex      USD
		expected BPS
	}{
		{"1% spread", NewUSD(100), NewUSD(101), NewBPSFromInt(100)},
		{"-1% spread", NewUSD(100), NewUSD(99), NewBPSFromInt(-100)},
		{"0% spread", NewUSD(100), NewUSD(100), NewBPSFromInt(0)},
		{"0.5% spread", NewUSD(2000), NewUSD(2010), NewBPSFromInt(50)},
		{"zero cex", Zero(), NewUSD(100), NewBPSFromInt(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateSpreadBPS(tt.cex, tt.dex)
			if result != tt.expected {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCalculateProfit(t *testing.T) {
	tests := []struct {
		name      string
		gross     USD
		gas       USD
		fees      USD
		expected  USD
		profitable bool
	}{
		{
			name:      "profitable",
			gross:     NewUSD(100),
			gas:       NewUSD(20),
			fees:      NewUSD(10),
			expected:  NewUSD(70),
			profitable: true,
		},
		{
			name:      "break even",
			gross:     NewUSD(100),
			gas:       NewUSD(50),
			fees:      NewUSD(50),
			expected:  Zero(),
			profitable: false,
		},
		{
			name:      "loss",
			gross:     NewUSD(50),
			gas:       NewUSD(40),
			fees:      NewUSD(20),
			expected:  NewUSD(-10),
			profitable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateProfit(tt.gross, tt.gas, tt.fees)
			if result != tt.expected {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
			if result.IsPositive() != tt.profitable {
				t.Errorf("IsPositive() = %v, want %v", result.IsPositive(), tt.profitable)
			}
		})
	}
}

func TestBPS(t *testing.T) {
	t.Run("NewBPS from percent", func(t *testing.T) {
		bps := NewBPS(0.5) // 0.5%
		if bps != 50 {
			t.Errorf("got %d, want 50", bps)
		}
	})

	t.Run("Percent string", func(t *testing.T) {
		bps := NewBPSFromInt(50)
		if got := bps.Percent(); got != "0.50%" {
			t.Errorf("got %q, want %q", got, "0.50%")
		}
	})

	t.Run("Abs", func(t *testing.T) {
		bps := NewBPSFromInt(-100)
		if bps.Abs() != 100 {
			t.Errorf("got %d, want 100", bps.Abs())
		}
	})
}

func TestGwei(t *testing.T) {
	g := NewGwei(50)

	if g.ToWei() != 50_000_000_000 {
		t.Errorf("ToWei: got %d, want 50000000000", g.ToWei())
	}

	if g.String() != "50.0 gwei" {
		t.Errorf("String: got %q, want %q", g.String(), "50.0 gwei")
	}
}

func TestCalculateFees(t *testing.T) {
	// 0.4% total fees (Binance 0.1% + Uniswap 0.3%)
	totalFeeBPS := NewBPSFromInt(40)
	tradeValue := NewUSD(10000) // $10,000 trade

	fees := CalculateFees(tradeValue, totalFeeBPS)
	expected := NewUSD(40) // $40

	if fees != expected {
		t.Errorf("got %v, want %v", fees, expected)
	}
}

// Benchmark to verify zero allocations
func BenchmarkUSDArithmetic(b *testing.B) {
	a := NewUSD(1234.56)
	c := NewUSD(789.01)
	bps := NewBPSFromInt(50)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = a.Add(c).Sub(c).MulBPS(bps)
	}
}

func BenchmarkCalculateSpread(b *testing.B) {
	cex := NewUSD(2500.00)
	dex := NewUSD(2512.50)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = CalculateSpreadBPS(cex, dex)
	}
}
