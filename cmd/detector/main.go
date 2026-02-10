package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/arbitrage"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/blockchain"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/notification"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/aws"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/config"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
	"golang.org/x/sync/semaphore"
)

// binanceAdapter adapts BinanceProvider to arbitrage.PriceProvider interface
type binanceAdapter struct {
	provider      *pricing.BinanceProvider
	symbol        string
	baseSymbol    string
	quoteSymbol   string
	baseDecimals  int
	quoteDecimals int
	quoteIsStable bool
}

func (b *binanceAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	// CEX doesn't use gas price or block number, ignore them
	price, err := b.provider.GetPrice(ctx, b.symbol, size, isBuy, b.baseDecimals, b.quoteDecimals)
	if err != nil {
		return nil, err
	}

	// Attach metadata for downstream calculations
	price.BaseSymbol = b.baseSymbol
	price.QuoteSymbol = b.quoteSymbol
	price.BaseDecimals = b.baseDecimals
	price.QuoteDecimals = b.quoteDecimals
	price.QuoteIsStable = b.quoteIsStable

	return price, nil
}

// GetETHPrice implements arbitrage.ETHPriceProvider interface
func (b *binanceAdapter) GetETHPrice(ctx context.Context) (float64, error) {
	return b.provider.GetETHPrice(ctx)
}

// uniswapAdapter adapts UniswapProvider to arbitrage.PriceProvider interface
type uniswapAdapter struct {
	provider      *pricing.UniswapProvider
	baseSymbol    string
	quoteSymbol   string
	baseDecimals  int
	quoteDecimals int
	quoteIsStable bool
}

func (u *uniswapAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	// Pass gas price and block number to DEX provider (for caching and gas estimation)
	price, err := u.provider.GetPrice(ctx, size, isBuy, gasPrice, blockNum)
	if err != nil {
		return nil, err
	}

	// Attach metadata for downstream calculations
	price.BaseSymbol = u.baseSymbol
	price.QuoteSymbol = u.quoteSymbol
	price.BaseDecimals = u.baseDecimals
	price.QuoteDecimals = u.quoteDecimals
	price.QuoteIsStable = u.quoteIsStable

	return price, nil
}

// createDetectorForPair creates a detector instance for a specific trading pair
func createDetectorForPair(
	ctx context.Context,
	pairCfg config.TradingPairConfig,
	binanceProvider *pricing.BinanceProvider,
	clientPool *blockchain.ClientPool,
	publisher *notification.Publisher,
	layeredCache *cache.LayeredCache,
	logger *observability.Logger,
	metrics *observability.Metrics,
	globalCfg *config.Config,
	dexQuoteLimiter *semaphore.Weighted,
) (*arbitrage.Detector, *pricing.UniswapProvider, error) {
	// Get Ethereum client from pool
	ethClient, err := clientPool.GetClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get Ethereum client: %w", err)
	}

	// Create Uniswap provider with token addresses from registry
	uniswapProvider, err := pricing.NewUniswapProvider(pricing.UniswapProviderConfig{
		Client:         ethClient,
		QuoterAddress:  globalCfg.Uniswap.QuoterAddress,
		PoolAddress:    globalCfg.Uniswap.PoolAddress, // Still used for cache key
		Token0Address:  pairCfg.Quote.Address,         // USDC/USDT from registry
		Token1Address:  pairCfg.Base.Address,          // WETH/WBTC from registry
		Token0Decimals: pairCfg.Quote.Decimals,
		Token1Decimals: pairCfg.Base.Decimals,
		PairName:       pairCfg.Name,
		Cache:          layeredCache,
		Logger:         logger,
		Metrics:        metrics,
		FeeTiers:       globalCfg.Uniswap.FeeTiers,
		RateLimitRPM:   globalCfg.Uniswap.RateLimit.RequestsPerMinute,
		RateLimitBurst: globalCfg.Uniswap.RateLimit.Burst,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Uniswap provider for %s: %w", pairCfg.Name, err)
	}

	// CEX symbol: "ETH-USDC" → "ETHUSDC"
	cexSymbol := config.FormatCEXSymbol(pairCfg.Name)
	logger.Info("creating detector for pair",
		"pair", pairCfg.Name,
		"cex_symbol", cexSymbol,
		"base_token", pairCfg.Base.Symbol,
		"quote_token", pairCfg.Quote.Symbol,
		"trade_sizes", len(pairCfg.GetParsedTradeSizes()),
	)

	// Create detector with adapters
	detector, err := arbitrage.NewDetector(arbitrage.DetectorConfig{
		CEXProvider: &binanceAdapter{
			provider:      binanceProvider,
			symbol:        cexSymbol,
			baseSymbol:    pairCfg.Base.Symbol,
			quoteSymbol:   pairCfg.Quote.Symbol,
			baseDecimals:  pairCfg.Base.Decimals,
			quoteDecimals: pairCfg.Quote.Decimals,
			quoteIsStable: pairCfg.Quote.IsStablecoin,
		},
		DEXProvider: &uniswapAdapter{
			provider:      uniswapProvider,
			baseSymbol:    pairCfg.Base.Symbol,
			quoteSymbol:   pairCfg.Quote.Symbol,
			baseDecimals:  pairCfg.Base.Decimals,
			quoteDecimals: pairCfg.Quote.Decimals,
			quoteIsStable: pairCfg.Quote.IsStablecoin,
		},
		Publisher:       publisher,
		Cache:           layeredCache,
		Logger:          logger,
		Metrics:         metrics,
		TradeSizes:      pairCfg.GetParsedTradeSizes(),
		MinProfitPct:    pairCfg.MinProfitThreshold,
		ETHPriceUSD:     2000, // Will be updated from Binance
		TradingSymbol:   cexSymbol,
		EthClient:       ethClient,
		DEXQuoteLimiter: dexQuoteLimiter,
		// Multi-pair support
		PairName:   pairCfg.Name,
		BaseToken:  pairCfg.Base,
		QuoteToken: pairCfg.Quote,
	})
	if err != nil {
		return nil, nil, err
	}
	return detector, uniswapProvider, nil
}

func main() {
	// Create root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	log.Println("Loading configuration...")
	cfg := config.MustLoad("config.yaml")

	// Setup observability (foundational - must be first)
	log.Println("Setting up observability...")
	logger := observability.NewLogger(cfg.Observability.Logging.Level, cfg.Observability.Logging.Format)

	metrics, err := observability.NewMetrics("arbitrage-detector", cfg.Observability.Metrics.Enabled)
	if err != nil {
		log.Fatalf("Failed to create metrics: %v", err)
	}

	tracer, err := observability.NewTracerProvider(ctx, "arbitrage-detector", cfg.Observability.Tracing.Endpoint, cfg.Observability.Tracing.Enabled)
	if err != nil {
		log.Fatalf("Failed to create tracer: %v", err)
	}
	defer tracer.Shutdown(ctx)

	logger.Info("observability setup complete")

	// Setup infrastructure
	logger.Info("setting up infrastructure...")

	// Redis cache
	redisCache, err := cache.NewRedisCache(cfg.Redis.Address, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		logger.LogError(ctx, "failed to create Redis cache", err)
		log.Fatalf("Failed to create Redis cache: %v", err)
	}
	defer redisCache.Close()

	// Memory cache
	memCache := cache.NewMemoryCache(cfg.Cache.L1MaxSize)
	defer memCache.Close()

	// Layered cache
	layeredCache := cache.NewLayeredCache(memCache, redisCache)

	// AWS configuration
	awsCfg, err := aws.LoadAWSConfig(ctx, aws.Config{
		Region:   cfg.AWS.Region,
		Endpoint: cfg.AWS.Endpoint,
	})
	if err != nil {
		logger.LogError(ctx, "failed to load AWS config", err)
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	// SNS client
	snsClient := aws.NewSNSClient(aws.SNSClientConfig{
		AWSConfig: awsCfg,
		Logger:    logger,
		Metrics:   metrics,
	})

	// Create Ethereum client pool
	logger.Info("connecting to Ethereum...")
	endpoints := make([]blockchain.EndpointConfig, len(cfg.Ethereum.RPCEndpoints))
	for i, ep := range cfg.Ethereum.RPCEndpoints {
		endpoints[i] = blockchain.EndpointConfig{
			URL:    ep.URL,
			Weight: ep.Weight,
		}
	}

	clientPool, err := blockchain.NewClientPool(blockchain.ClientPoolConfig{
		Endpoints: endpoints,
		Logger:    logger,
		Metrics:   metrics,
	})
	if err != nil {
		logger.LogError(ctx, "failed to create client pool", err)
		log.Fatalf("Failed to create client pool: %v", err)
	}
	defer clientPool.Close()

	// Global DEX quote concurrency limiter (shared across all detectors)
	var dexQuoteLimiter *semaphore.Weighted
	if cfg.Arbitrage.MaxConcurrentDEXQuotes > 0 {
		dexQuoteLimiter = semaphore.NewWeighted(int64(cfg.Arbitrage.MaxConcurrentDEXQuotes))
	}

	// Create price providers
	logger.Info("creating price providers...")

	// Binance provider
	binanceProvider, err := pricing.NewBinanceProvider(pricing.BinanceProviderConfig{
		BaseURL:        cfg.Exchanges.Binance.BaseURL,
		RateLimitRPM:   cfg.Exchanges.Binance.RateLimit.RequestsPerMinute,
		RateLimitBurst: cfg.Exchanges.Binance.RateLimit.Burst,
		Cache:          layeredCache,
		Logger:         logger,
		Metrics:        metrics,
		TradingFee:     0.001, // 0.1% Binance fee
	})
	if err != nil {
		logger.LogError(ctx, "failed to create Binance provider", err)
		log.Fatalf("Failed to create Binance provider: %v", err)
	}

	// Create notification publisher
	logger.Info("creating notification publisher...")
	publisher, err := notification.NewPublisher(notification.PublisherConfig{
		SNSClient: snsClient,
		TopicARN:  cfg.AWS.SNSTopicARN,
		Logger:    logger,
		Metrics:   metrics,
	})
	if err != nil {
		logger.LogError(ctx, "failed to create publisher", err)
		log.Fatalf("Failed to create publisher: %v", err)
	}

	// Create arbitrage detectors (one per trading pair)
	logger.Info("creating arbitrage detectors for trading pairs...")
	detectors := make(map[string]*arbitrage.Detector)
	dexProviders := make(map[string]*pricing.UniswapProvider)

	for _, pairCfg := range cfg.Arbitrage.GetParsedPairs() {
		detector, dexProvider, err := createDetectorForPair(
			ctx,
			pairCfg,
			binanceProvider,
			clientPool,
			publisher,
			layeredCache,
			logger,
			metrics,
			cfg,
			dexQuoteLimiter,
		)
		if err != nil {
			logger.LogError(ctx, "failed to create detector for pair", err, "pair", pairCfg.Name)
			log.Fatalf("Failed to create detector for pair %s: %v", pairCfg.Name, err)
		}
		detectors[pairCfg.Name] = detector
		dexProviders[pairCfg.Name] = dexProvider
		logger.Info("detector created", "pair", pairCfg.Name)
	}

	logger.Info("all detectors created", "count", len(detectors))

	// Warm up caches before starting block processing
	logger.Info("warming caches...")
	warmer := cache.NewWarmer(logger, cache.DefaultWarmupConfig())
	warmer.RegisterProvider(binanceProvider)
	for _, provider := range dexProviders {
		warmer.RegisterProvider(provider)
	}
	warmupResults := warmer.Warmup(ctx)
	if warmupResults.HasErrors() {
		logger.LogWarn(ctx, fmt.Sprintf("cache warmup completed with %d errors", warmupResults.Errors))
	}

	// Create block subscriber
	logger.Info("creating block subscriber...")
	subscriber, err := blockchain.NewSubscriber(blockchain.SubscriberConfig{
		WebSocketURLs:   cfg.Ethereum.WebSocketURLs,
		Logger:          logger,
		Metrics:         metrics,
		ReconnectConfig: blockchain.DefaultReconnectConfig(),
		ClientPool:      clientPool, // NEW: for HTTP RPC fallback
	})
	if err != nil {
		logger.LogError(ctx, "failed to create subscriber", err)
		log.Fatalf("Failed to create subscriber: %v", err)
	}

	// Start HTTP server for health checks and metrics
	logger.Info("starting HTTP server...")
	httpServer := startHTTPServer(cfg.HTTP.Port, metrics, logger, clientPool, binanceProvider, dexProviders)

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Run application
	logger.Info("starting arbitrage detector application...")
	go func() {
		if err := runDetector(ctx, detectors, subscriber, logger, metrics); err != nil {
			logger.LogError(ctx, "detector error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	<-sigCh
	logger.Info("shutdown signal received, gracefully stopping...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP server first (stop accepting new requests)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.LogError(shutdownCtx, "HTTP server shutdown error", err)
	} else {
		logger.Info("HTTP server stopped gracefully")
	}

	// Close block subscriber
	subscriber.Close()
	logger.Info("block subscriber closed")

	// Cancel context to stop detector goroutines
	cancel()

	logger.Info("application stopped")
}

// runDetector runs the main detector loop with multiple detectors (one per pair)
func runDetector(
	ctx context.Context,
	detectors map[string]*arbitrage.Detector,
	subscriber *blockchain.Subscriber,
	logger *observability.Logger,
	metrics *observability.Metrics,
) error {
	// Subscribe to new blocks
	blockCh, errCh, err := subscriber.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to blocks: %w", err)
	}

	logger.Info("subscribed to block headers, waiting for blocks...", "detectors", len(detectors))

	// Process blocks
	for {
		select {
		case block := <-blockCh:
			start := time.Now()

			logger.Info("processing block",
				"block_number", block.Number.Uint64(),
				"block_hash", block.Hash.Hex(),
				"timestamp", block.Timestamp,
			)

			// Detect arbitrage opportunities for all pairs concurrently
			type pairResult struct {
				pair          string
				opportunities []*arbitrage.Opportunity
				err           error
			}

			resultsCh := make(chan pairResult, len(detectors))

			// Launch detectors for each pair concurrently
			for pairName, detector := range detectors {
				go func(name string, det *arbitrage.Detector) {
					opps, err := det.Detect(ctx, block.Number.Uint64(), block.Timestamp)
					resultsCh <- pairResult{
						pair:          name,
						opportunities: opps,
						err:           err,
					}
				}(pairName, detector)
			}

			// Collect results from all detectors
			totalOpportunities := 0
			for i := 0; i < len(detectors); i++ {
				result := <-resultsCh
				if result.err != nil {
					logger.LogError(ctx, "detection failed for pair", result.err,
						"block", block.Number.Uint64(),
						"pair", result.pair,
					)
					continue
				}

				// Log opportunities to console
				for _, opp := range result.opportunities {
					if opp.IsProfitable() {
						// Print formatted opportunity
						fmt.Println(opp.FormatOutput())
					}
				}

				totalOpportunities += len(result.opportunities)
			}

			// Record block processing time
			duration := time.Since(start)
			logger.Info("block processed",
				"block", block.Number.Uint64(),
				"opportunities", totalOpportunities,
				"pairs", len(detectors),
				"duration_ms", duration.Milliseconds(),
			)

		case err := <-errCh:
			logger.LogError(ctx, "block subscription error", err)
			// Subscriber will auto-reconnect

		case <-ctx.Done():
			logger.Info("context cancelled, stopping detector")
			return nil
		}
	}
}

// startHTTPServer starts HTTP server for health checks and metrics
// Returns the server instance for graceful shutdown
func startHTTPServer(port int, metrics *observability.Metrics, logger *observability.Logger, clientPool *blockchain.ClientPool, binanceProvider *pricing.BinanceProvider, dexProviders map[string]*pricing.UniswapProvider) *http.Server {
	mux := http.NewServeMux()

	type providerHealthResponse struct {
		Status              string `json:"status"`
		Provider            string `json:"provider,omitempty"`
		Pair                string `json:"pair,omitempty"`
		LastSuccess         string `json:"last_success,omitempty"`
		LastFailure         string `json:"last_failure,omitempty"`
		LastError           string `json:"last_error,omitempty"`
		LastDurationMs      int64  `json:"last_duration_ms,omitempty"`
		ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
		CircuitState        string `json:"circuit_state,omitempty"`
		AgeSeconds          int64  `json:"age_seconds,omitempty"`
	}

	type rpcHealthResponse struct {
		Status         string          `json:"status"`
		HealthyCount   int             `json:"healthy_count"`
		TotalCount     int             `json:"total_count"`
		EndpointStatus map[string]bool `json:"endpoint_status"`
	}

	type healthResponse struct {
		Status    string                            `json:"status"`
		Timestamp string                            `json:"timestamp"`
		RPC       rpcHealthResponse                 `json:"rpc"`
		CEX       providerHealthResponse            `json:"cex"`
		DEX       map[string]providerHealthResponse `json:"dex"`
	}

	const (
		providerStaleAfter     = 30 * time.Second
		providerUnhealthyAfter = 2 * time.Minute
	)

	deriveProviderStatus := func(h pricing.ProviderHealth, now time.Time) providerHealthResponse {
		status := "unknown"
		var ageSeconds int64
		if !h.LastSuccess.IsZero() {
			age := now.Sub(h.LastSuccess)
			ageSeconds = int64(age.Seconds())
			if age > providerUnhealthyAfter {
				status = "unhealthy"
			} else if age > providerStaleAfter || h.ConsecutiveFailures > 0 {
				status = "degraded"
			} else {
				status = "healthy"
			}
		}

		resp := providerHealthResponse{
			Status:              status,
			Provider:            h.Provider,
			Pair:                h.Pair,
			LastDurationMs:      h.LastDuration.Milliseconds(),
			ConsecutiveFailures: h.ConsecutiveFailures,
			CircuitState:        h.CircuitState,
			AgeSeconds:          ageSeconds,
		}
		if !h.LastSuccess.IsZero() {
			resp.LastSuccess = h.LastSuccess.UTC().Format(time.RFC3339)
		}
		if !h.LastFailure.IsZero() {
			resp.LastFailure = h.LastFailure.UTC().Format(time.RFC3339)
		}
		if h.LastError != "" {
			resp.LastError = h.LastError
		}
		return resp
	}

	mergeStatus := func(statuses ...string) string {
		hasDegraded := false
		for _, s := range statuses {
			if s == "unhealthy" {
				return "unhealthy"
			}
			if s == "degraded" || s == "unknown" {
				hasDegraded = true
			}
		}
		if hasDegraded {
			return "degraded"
		}
		return "healthy"
	}

	buildHealth := func() healthResponse {
		now := time.Now().UTC()

		endpoints := map[string]bool{}
		healthyCount := 0
		totalCount := 0
		if clientPool != nil {
			endpoints = clientPool.GetEndpointStatus()
			totalCount = len(endpoints)
			healthyCount = clientPool.GetHealthyEndpointCount()
		}

		rpcStatus := "unknown"
		if totalCount == 0 {
			rpcStatus = "unhealthy"
		} else if healthyCount == 0 {
			rpcStatus = "unhealthy"
		} else if healthyCount < totalCount {
			rpcStatus = "degraded"
		} else {
			rpcStatus = "healthy"
		}

		cexHealth := providerHealthResponse{Status: "unknown"}
		if binanceProvider != nil {
			cexHealth = deriveProviderStatus(binanceProvider.Health(), now)
		}

		dexHealth := make(map[string]providerHealthResponse)
		for pair, provider := range dexProviders {
			if provider == nil {
				continue
			}
			dexHealth[pair] = deriveProviderStatus(provider.Health(), now)
		}

		statuses := []string{rpcStatus, cexHealth.Status}
		for _, dh := range dexHealth {
			statuses = append(statuses, dh.Status)
		}

		return healthResponse{
			Status:    mergeStatus(statuses...),
			Timestamp: now.Format(time.RFC3339),
			RPC: rpcHealthResponse{
				Status:         rpcStatus,
				HealthyCount:   healthyCount,
				TotalCount:     totalCount,
				EndpointStatus: endpoints,
			},
			CEX: cexHealth,
			DEX: dexHealth,
		}
	}

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health := buildHealth()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(health)
	})

	// Readiness check
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		health := buildHealth()
		w.Header().Set("Content-Type", "application/json")
		if health.Status == "unhealthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(health)
	})

	// Metrics endpoint
	mux.Handle("/metrics", metrics.Handler())

	addr := fmt.Sprintf(":%d", port)
	logger.Info("HTTP server listening", "address", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.LogError(context.Background(), "HTTP server error", err)
		}
	}()

	return server
}
