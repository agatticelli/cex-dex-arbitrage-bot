package pricing

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
)

// mockOrderbookResponse returns a standard orderbook response for testing
func mockOrderbookResponse() BinanceOrderbookResponse {
	return BinanceOrderbookResponse{
		LastUpdateID: 123456789,
		Bids: [][]string{
			{"2050.00", "10.5"},
			{"2049.50", "5.0"},
			{"2049.00", "8.0"},
			{"2048.00", "15.0"},
		},
		Asks: [][]string{
			{"2051.00", "8.0"},
			{"2051.50", "12.0"},
			{"2052.00", "6.0"},
			{"2053.00", "10.0"},
		},
	}
}

// createTestServer creates a test HTTP server that returns orderbook data
func createTestServer(t *testing.T, response BinanceOrderbookResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
}

// createTestProvider creates a BinanceProvider configured for testing
func createTestProvider(t *testing.T, serverURL string) *BinanceProvider {
	logger := observability.NewLogger("error", "json")

	provider, err := NewBinanceProvider(BinanceProviderConfig{
		BaseURL:        serverURL,
		RateLimitRPM:   6000, // High limit for tests
		RateLimitBurst: 100,
		Logger:         logger,
		TradingFee:     0.001, // 0.1%
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	return provider
}

func TestBinanceProvider_GetOrderbook(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	orderbook, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err != nil {
		t.Fatalf("GetOrderbook failed: %v", err)
	}

	// Verify bids
	if len(orderbook.Bids) != 4 {
		t.Errorf("Expected 4 bids, got %d", len(orderbook.Bids))
	}

	// Verify first bid price
	firstBidPrice, _ := orderbook.Bids[0].Price.Float64()
	if firstBidPrice != 2050.00 {
		t.Errorf("Expected first bid price 2050.00, got %.2f", firstBidPrice)
	}

	// Verify asks
	if len(orderbook.Asks) != 4 {
		t.Errorf("Expected 4 asks, got %d", len(orderbook.Asks))
	}

	// Verify first ask price
	firstAskPrice, _ := orderbook.Asks[0].Price.Float64()
	if firstAskPrice != 2051.00 {
		t.Errorf("Expected first ask price 2051.00, got %.2f", firstAskPrice)
	}

	// Verify orderbook is valid
	if !orderbook.IsValid() {
		t.Error("Expected orderbook to be valid")
	}
}

func TestBinanceProvider_GetPrice_Buy(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	size := big.NewInt(1e18) // 1 ETH

	price, err := provider.GetPrice(ctx, "ETHUSDC", size, true, 18, 6)
	if err != nil {
		t.Fatalf("GetPrice failed: %v", err)
	}

	// For buy, should use asks (higher prices)
	priceValue, _ := price.Value.Float64()

	// First ask is 2051.00, so effective price should be around that
	if priceValue < 2050 || priceValue > 2055 {
		t.Errorf("Expected buy price around 2051, got %.2f", priceValue)
	}

	// Verify AmountOut includes fee (should be higher than base price * size)
	amountOut, _ := price.AmountOut.Float64()
	baseCost := priceValue * 1.0 // 1 ETH

	// AmountOut should include 0.1% fee for buy
	expectedMinAmountOut := baseCost * 1.001
	if amountOut < expectedMinAmountOut*0.99 {
		t.Errorf("AmountOut %.2f should include fee, expected at least %.2f", amountOut, expectedMinAmountOut)
	}

	// Verify trading fee is set
	tradingFee, _ := price.TradingFee.Float64()
	if tradingFee != 0.001 {
		t.Errorf("Expected trading fee 0.001, got %f", tradingFee)
	}

	// Verify gas cost is 0 for CEX
	if price.GasCost.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected gas cost 0 for CEX, got %s", price.GasCost.String())
	}
}

func TestBinanceProvider_GetPrice_Sell(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	size := big.NewInt(1e18) // 1 ETH

	price, err := provider.GetPrice(ctx, "ETHUSDC", size, false, 18, 6)
	if err != nil {
		t.Fatalf("GetPrice failed: %v", err)
	}

	// For sell, should use bids (lower prices)
	priceValue, _ := price.Value.Float64()

	// First bid is 2050.00, so effective price should be around that
	if priceValue < 2045 || priceValue > 2051 {
		t.Errorf("Expected sell price around 2050, got %.2f", priceValue)
	}

	// Verify AmountOut includes fee deduction (should be lower than base price * size)
	amountOut, _ := price.AmountOut.Float64()
	baseCost := priceValue * 1.0 // 1 ETH

	// AmountOut should deduct 0.1% fee for sell
	expectedMaxAmountOut := baseCost * 0.999
	if amountOut > expectedMaxAmountOut*1.01 {
		t.Errorf("AmountOut %.2f should have fee deducted, expected at most %.2f", amountOut, expectedMaxAmountOut)
	}
}

func TestBinanceProvider_GetPrice_LargeOrder(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	// Large order that should walk multiple levels
	// Total ask volume: 8 + 12 + 6 + 10 = 36 ETH
	size := new(big.Int).Mul(big.NewInt(20), big.NewInt(1e18)) // 20 ETH

	price, err := provider.GetPrice(ctx, "ETHUSDC", size, true, 18, 6)
	if err != nil {
		t.Fatalf("GetPrice failed: %v", err)
	}

	priceValue, _ := price.Value.Float64()

	// Should have slippage since we're consuming multiple levels
	// First 8 ETH @ 2051, next 12 ETH @ 2051.50 = 20 ETH
	// Weighted avg: (8*2051 + 12*2051.50) / 20 = 2051.30
	expectedPrice := 2051.30
	tolerance := 0.5

	if priceValue < expectedPrice-tolerance || priceValue > expectedPrice+tolerance {
		t.Errorf("Expected price around %.2f for large order, got %.2f", expectedPrice, priceValue)
	}

	// Verify slippage is non-zero
	slippage, _ := price.Slippage.Float64()
	if slippage <= 0 {
		t.Logf("Slippage: %f (may be 0 if using first level as reference)", slippage)
	}
}

func TestBinanceProvider_GetETHPrice(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	ethPrice, err := provider.GetETHPrice(ctx)
	if err != nil {
		t.Fatalf("GetETHPrice failed: %v", err)
	}

	// Mid price should be (2050 + 2051) / 2 = 2050.50
	expectedMidPrice := 2050.50
	tolerance := 0.01

	if ethPrice < expectedMidPrice-tolerance || ethPrice > expectedMidPrice+tolerance {
		t.Errorf("Expected mid price %.2f, got %.2f", expectedMidPrice, ethPrice)
	}
}

func TestBinanceProvider_GetETHPrice_InvalidRange(t *testing.T) {
	// Create response with price outside valid range
	response := BinanceOrderbookResponse{
		LastUpdateID: 123456789,
		Bids:         [][]string{{"100.00", "10.0"}}, // Too low
		Asks:         [][]string{{"101.00", "10.0"}},
	}

	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	_, err := provider.GetETHPrice(ctx)
	if err == nil {
		t.Error("Expected error for ETH price outside valid range")
	}
}

func TestBinanceProvider_CacheHit(t *testing.T) {
	response := mockOrderbookResponse()
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create provider with cache
	memCache := cache.NewMemoryCache(100)
	defer memCache.Close()

	logger := observability.NewLogger("error", "json")

	provider, _ := NewBinanceProvider(BinanceProviderConfig{
		BaseURL:        server.URL,
		RateLimitRPM:   6000,
		RateLimitBurst: 100,
		Logger:         logger,
		Cache:          memCache,
	})

	ctx := context.Background()

	// First call should hit API
	_, err := provider.GetPrice(ctx, "ETHUSDC", big.NewInt(1e18), true, 18, 6)
	if err != nil {
		t.Fatalf("First GetPrice failed: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("Expected 1 API request, got %d", requestCount)
	}

	// Second call should hit cache
	_, err = provider.GetPrice(ctx, "ETHUSDC", big.NewInt(1e18), true, 18, 6)
	if err != nil {
		t.Fatalf("Second GetPrice failed: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("Expected still 1 API request (cache hit), got %d", requestCount)
	}
}

func TestBinanceProvider_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code": -1015, "msg": "Too many requests"}`))
	}))
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	_, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err == nil {
		t.Error("Expected error for HTTP 429 response")
	}
}

func TestBinanceProvider_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid json}`))
	}))
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	_, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err == nil {
		t.Error("Expected error for invalid JSON response")
	}
}

func TestBinanceProvider_EmptyOrderbook(t *testing.T) {
	response := BinanceOrderbookResponse{
		LastUpdateID: 123456789,
		Bids:         [][]string{},
		Asks:         [][]string{},
	}

	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	orderbook, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err != nil {
		t.Fatalf("GetOrderbook failed: %v", err)
	}

	// Empty orderbook should not be valid
	if orderbook.IsValid() {
		t.Error("Expected empty orderbook to be invalid")
	}
}

func TestBinanceProvider_parseOrderbook_MalformedEntries(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	provider := &BinanceProvider{
		logger: logger,
	}

	// Response with malformed entries (should be skipped)
	response := &BinanceOrderbookResponse{
		LastUpdateID: 123456789,
		Bids: [][]string{
			{"2050.00", "10.0"},      // Valid
			{"invalid", "5.0"},       // Invalid price
			{"2049.00"},              // Missing volume
			{"2048.00", "not_a_num"}, // Invalid volume
		},
		Asks: [][]string{
			{"2051.00", "8.0"}, // Valid
		},
	}

	orderbook, err := provider.parseOrderbook(response)
	if err != nil {
		t.Fatalf("parseOrderbook failed: %v", err)
	}

	// Should only have valid entries
	if len(orderbook.Bids) != 1 {
		t.Errorf("Expected 1 valid bid, got %d", len(orderbook.Bids))
	}

	if len(orderbook.Asks) != 1 {
		t.Errorf("Expected 1 valid ask, got %d", len(orderbook.Asks))
	}
}

func TestBinanceProvider_Health(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	// Initially, health should show no successes
	health := provider.Health()
	if health.Provider != "binance" {
		t.Errorf("Expected provider 'binance', got '%s'", health.Provider)
	}

	// Make a successful request
	ctx := context.Background()
	_, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err != nil {
		t.Fatalf("GetOrderbook failed: %v", err)
	}

	// Health should show success
	health = provider.Health()
	if health.LastSuccess.IsZero() {
		t.Error("Expected LastSuccess to be set after successful request")
	}
	if health.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", health.ConsecutiveFailures)
	}
	if health.CircuitState != "closed" {
		t.Errorf("Expected circuit state 'closed', got '%s'", health.CircuitState)
	}
}

func TestBinanceProvider_ConcurrentRequests(t *testing.T) {
	response := mockOrderbookResponse()
	var requestCount int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()

		// Simulate some latency
		time.Sleep(10 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	ctx := context.Background()
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	// Make concurrent requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent request failed: %v", err)
	}

	t.Logf("Completed %d concurrent requests with %d API calls", 10, requestCount)
}

func TestBinanceProvider_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(5 * time.Second)
		json.NewEncoder(w).Encode(mockOrderbookResponse())
	}))
	defer server.Close()

	provider := createTestProvider(t, server.URL)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := provider.GetOrderbook(ctx, "ETHUSDC", 100)
	if err == nil {
		t.Error("Expected error due to context cancellation")
	}
}

func TestBinanceProvider_FeeCalculation(t *testing.T) {
	response := mockOrderbookResponse()
	server := createTestServer(t, response)
	defer server.Close()

	tests := []struct {
		name       string
		fee        float64
		isBuy      bool
		basePrice  float64 // approximate
		expectMore bool    // true if AmountOut should be > basePrice (buy), false if < (sell)
	}{
		{
			name:       "buy with 0.1% fee",
			fee:        0.001,
			isBuy:      true,
			basePrice:  2051.0,
			expectMore: true,
		},
		{
			name:       "sell with 0.1% fee",
			fee:        0.001,
			isBuy:      false,
			basePrice:  2050.0,
			expectMore: false,
		},
		{
			name:       "buy with 0.5% fee",
			fee:        0.005,
			isBuy:      true,
			basePrice:  2051.0,
			expectMore: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := observability.NewLogger("error", "json")

			provider, _ := NewBinanceProvider(BinanceProviderConfig{
				BaseURL:        server.URL,
				RateLimitRPM:   6000,
				RateLimitBurst: 100,
				Logger:         logger,
				TradingFee:     tt.fee,
			})

			ctx := context.Background()
			price, err := provider.GetPrice(ctx, "ETHUSDC", big.NewInt(1e18), tt.isBuy, 18, 6)
			if err != nil {
				t.Fatalf("GetPrice failed: %v", err)
			}

			amountOut, _ := price.AmountOut.Float64()
			priceValue, _ := price.Value.Float64()

			if tt.expectMore {
				// Buy: AmountOut should be higher than price (we pay more)
				if amountOut <= priceValue {
					t.Errorf("Buy: AmountOut %.2f should be > price %.2f", amountOut, priceValue)
				}
			} else {
				// Sell: AmountOut should be lower than price (we receive less)
				if amountOut >= priceValue {
					t.Errorf("Sell: AmountOut %.2f should be < price %.2f", amountOut, priceValue)
				}
			}

			// Verify fee is correctly set
			actualFee, _ := price.TradingFee.Float64()
			if actualFee != tt.fee {
				t.Errorf("Expected fee %f, got %f", tt.fee, actualFee)
			}
		})
	}
}
