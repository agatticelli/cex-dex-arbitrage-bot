package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gatti/cex-dex-arbitrage-bot/internal/arbitrage"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/blockchain"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/notification"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/aws"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/config"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/pricing"
)

// binanceAdapter adapts BinanceProvider to arbitrage.PriceProvider interface
type binanceAdapter struct {
	provider *pricing.BinanceProvider
	symbol   string
}

func (b *binanceAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int) (*pricing.Price, error) {
	// CEX doesn't use gas price, ignore it
	return b.provider.GetPrice(ctx, b.symbol, size, isBuy)
}

// uniswapAdapter adapts UniswapProvider to arbitrage.PriceProvider interface
type uniswapAdapter struct {
	provider *pricing.UniswapProvider
}

func (u *uniswapAdapter) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int) (*pricing.Price, error) {
	// Pass gas price to DEX provider (avoids expensive eth_gasPrice RPC call)
	return u.provider.GetPrice(ctx, size, isBuy, gasPrice)
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

	// Get first healthy client for Uniswap
	ethClient, err := clientPool.GetClient()
	if err != nil {
		logger.LogError(ctx, "failed to get Ethereum client", err)
		log.Fatalf("Failed to get Ethereum client: %v", err)
	}

	// Uniswap provider (using QuoterV2 for accurate pricing)
	uniswapProvider, err := pricing.NewUniswapProvider(pricing.UniswapProviderConfig{
		Client:        ethClient,
		QuoterAddress: cfg.Uniswap.QuoterAddress,
		PoolAddress:   cfg.Uniswap.PoolAddress,
		Token0Address: cfg.Uniswap.USDCAddress,
		Token1Address: cfg.Uniswap.WETHAddress,
		Cache:         layeredCache,
		Logger:        logger,
		Metrics:       metrics,
		FeePips:       3000, // 0.3% Uniswap V3 fee
	})
	if err != nil {
		logger.LogError(ctx, "failed to create Uniswap provider", err)
		log.Fatalf("Failed to create Uniswap provider: %v", err)
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

	// Create arbitrage detector with adapters
	logger.Info("creating arbitrage detector...")
	detector, err := arbitrage.NewDetector(arbitrage.DetectorConfig{
		CEXProvider: &binanceAdapter{
			provider: binanceProvider,
			symbol:   "ETHUSDC",
		},
		DEXProvider: &uniswapAdapter{
			provider: uniswapProvider,
		},
		Publisher:     publisher,
		Cache:         layeredCache,
		Logger:        logger,
		Metrics:       metrics,
		TradeSizes:    cfg.Arbitrage.GetParsedTradeSizes(),
		MinProfitPct:  cfg.Arbitrage.MinProfitThreshold,
		ETHPriceUSD:   2000, // TODO: Fetch real ETH price
		TradingSymbol: "ETHUSDC",
		EthClient:     ethClient, // For gas price fetching with caching
	})
	if err != nil {
		logger.LogError(ctx, "failed to create detector", err)
		log.Fatalf("Failed to create detector: %v", err)
	}

	// Create block subscriber
	logger.Info("creating block subscriber...")
	subscriber, err := blockchain.NewSubscriber(blockchain.SubscriberConfig{
		WebSocketURLs:   cfg.Ethereum.WebSocketURLs,
		Logger:          logger,
		Metrics:         metrics,
		ReconnectConfig: blockchain.DefaultReconnectConfig(),
	})
	if err != nil {
		logger.LogError(ctx, "failed to create subscriber", err)
		log.Fatalf("Failed to create subscriber: %v", err)
	}

	// Start HTTP server for health checks and metrics
	logger.Info("starting HTTP server...")
	go startHTTPServer(cfg.HTTP.Port, metrics, logger)

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Run application
	logger.Info("starting arbitrage detector application...")
	go func() {
		if err := runDetector(ctx, detector, subscriber, logger, metrics); err != nil {
			logger.LogError(ctx, "detector error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	<-sigCh
	logger.Info("shutdown signal received, gracefully stopping...")

	// Graceful shutdown
	subscriber.Close()
	logger.Info("application stopped")
}

// runDetector runs the main detector loop
func runDetector(
	ctx context.Context,
	detector *arbitrage.Detector,
	subscriber *blockchain.Subscriber,
	logger *observability.Logger,
	metrics *observability.Metrics,
) error {
	// Subscribe to new blocks
	blockCh, errCh, err := subscriber.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to blocks: %w", err)
	}

	logger.Info("subscribed to block headers, waiting for blocks...")

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

			// Detect arbitrage opportunities
			opportunities, err := detector.Detect(ctx, block.Number.Uint64(), block.Timestamp)
			if err != nil {
				logger.LogError(ctx, "detection failed", err,
					"block", block.Number.Uint64(),
				)
				continue
			}

			// Log opportunities to console
			for _, opp := range opportunities {
				if opp.IsProfitable() {
					// Print formatted opportunity
					fmt.Println(opp.FormatOutput())
				}
			}

			// Record block processing time
			duration := time.Since(start)
			logger.Info("block processed",
				"block", block.Number.Uint64(),
				"opportunities", len(opportunities),
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
func startHTTPServer(port int, metrics *observability.Metrics, logger *observability.Logger) {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	// Readiness check
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ready"}`))
	})

	// Metrics endpoint
	mux.Handle("/metrics", metrics.Handler())

	addr := fmt.Sprintf(":%d", port)
	logger.Info("HTTP server listening", "address", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.LogError(context.Background(), "HTTP server error", err)
	}
}
