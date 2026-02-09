# CEX-DEX Arbitrage Bot

A production-grade, real-time arbitrage detection system built in Go that monitors price discrepancies between Binance (CEX) and Uniswap V3 (DEX) for ETH-USDC trading pairs.

## Architecture Overview

This system demonstrates **senior-level software engineering** through:
- ✅ **Package-Oriented Design** (not layer-based architecture)
- ✅ **Direct Uniswap V3 Pool State Calculation** (advanced, not QuoterV2 beginner approach)
- ✅ **Comprehensive Observability** (OpenTelemetry, Prometheus, Jaeger)
- ✅ **Resilience Patterns** (circuit breaker, retry with exponential backoff, rate limiting)
- ✅ **Event-Driven Architecture** (SNS/SQS fan-out with LocalStack)
- ✅ **Parallel Price Fetching** (concurrent CEX and DEX quote retrieval)
- ✅ **Docker Compose Deployment** (production-ready containerized setup)

### High-Level Flow

```
Ethereum Block (WebSocket)
    ↓
[Arbitrage Detector]
    ↓ (parallel fetch)
    ├─→ Binance API (orderbook calculation)
    └─→ Uniswap V3 Pool (direct state calculation)
    ↓
[Profit Calculator]
    ↓ (if profitable)
    ↓
[SNS Topic] → [SQS Queues] → [Lambda Functions] → [DynamoDB + Webhooks]
```

### Package Structure (Package-Oriented Design)

```
cex-dex-arbitrage-bot/
├── cmd/
│   └── detector/main.go                # Dependency injection & application wiring
├── internal/
│   ├── arbitrage/                      # Core business logic (domain layer)
│   │   ├── detector.go                 # Arbitrage detection + interfaces (DIP)
│   │   ├── calculator.go               # Profit calculations
│   │   └── opportunity.go              # Domain model with formatters
│   ├── pricing/                        # Price providers (infrastructure)
│   │   ├── binance.go                  # CEX provider + Price type
│   │   ├── orderbook.go                # Orderbook calculation logic
│   │   ├── uniswap.go                  # DEX provider with direct pool state
│   │   └── uniswapv3/                  # Advanced Uniswap V3 math
│   │       ├── tick_math.go            # Tick ↔ sqrt price conversions
│   │       ├── sqrt_price_math.go      # Amount calculations
│   │       └── swap_math.go            # Swap simulation
│   ├── blockchain/                     # Ethereum integration
│   │   ├── subscriber.go               # WebSocket block subscription
│   │   └── client_pool.go              # RPC failover with health tracking
│   ├── notification/                   # Event publishing
│   │   └── publisher.go                # SNS integration with circuit breaker
│   └── platform/                       # Shared infrastructure (foundation)
│       ├── observability/              # Logging, metrics, tracing
│       ├── cache/                      # L1 (memory) + L2 (Redis) cache
│       ├── resilience/                 # Circuit breaker, retry, rate limiter
│       ├── aws/                        # SNS client with resilience
│       └── config/                     # Configuration management (viper)
├── scripts/localstack/                 # LocalStack initialization
├── docker-compose.yml                  # Local development stack
├── Makefile                            # Development commands
└── config.example.yaml                 # Configuration template
```

## Key Technical Decisions

### 1. Direct Uniswap V3 Pool State Calculation (Advanced Approach)

**Why Not QuoterV2?** The QuoterV2 contract is the beginner-friendly approach. This implementation demonstrates **deep protocol understanding** by:

- **Porting Solidity Math to Go**: Implementing `TickMath`, `SqrtPriceMath`, and `SwapMath` libraries from Uniswap V3 core contracts
- **Direct Pool State Reading**: Calling `slot0()` and `liquidity()` directly from the pool contract
- **Price Simulation**: Calculating output amounts by simulating swaps through the concentrated liquidity math
- **No External Dependencies**: One less point of failure, faster execution, full control over calculations

**Implementation Files**:
- `internal/pricing/uniswapv3/tick_math.go`: Binary search for tick discovery, sqrt price calculations
- `internal/pricing/uniswapv3/sqrt_price_math.go`: Token amount deltas based on liquidity and price ranges
- `internal/pricing/uniswapv3/swap_math.go`: Single-step swap simulation with fee accounting

### 2. Package-Oriented Design (Go Idiomatic)

**Not Layer-Based Architecture** - This isn't MVC or Clean Architecture with separate `models/`, `services/`, `repositories/` directories.

**Key Principles Applied**:
- **Types live with their implementations**: `Price` in `binance.go`, `Orderbook` in `orderbook.go`, `Opportunity` in `opportunity.go`
- **Interfaces defined where consumed**: `PriceProvider` interface in `detector.go` (consumer), not in a separate `interfaces.go`
- **Packages represent capabilities**: `pricing` provides pricing, `arbitrage` detects arbitrage, `blockchain` handles Ethereum
- **Dependency Inversion**: `arbitrage` package defines interfaces it needs; implementations in other packages

### 3. Parallel Price Fetching with errgroup

```go
// Fetch CEX and DEX prices concurrently
g, gctx := errgroup.WithContext(ctx)

g.Go(func() error { cexPrice, err = provider.GetPrice(gctx, ...); return err })
g.Go(func() error { dexPrice, err = provider.GetPrice(gctx, ...); return err })

if err := g.Wait(); err != nil { /* handle */ }
```

- **Reduces latency**: CEX and DEX calls happen simultaneously
- **Fail-fast**: First error cancels remaining goroutines via context
- **Type-safe**: errgroup properly handles errors from concurrent operations

### 4. Comprehensive Observability

**OpenTelemetry Integration**:
- **Metrics**: Prometheus-compatible metrics (block processing time, opportunities detected, cache hit ratio, circuit breaker states)
- **Tracing**: OTLP gRPC to Jaeger for distributed tracing across components
- **Logging**: Structured logging (slog) with trace context injection

**Key Metrics**:
- `arbitrage.block.processing.duration`: Histogram of block processing latency
- `arbitrage.opportunities.detected`: Counter with labels (direction, profitable)
- `arbitrage.cache.hit_ratio`: Gauge per layer (L1/L2)
- `arbitrage.circuit_breaker.state`: Gauge per service (closed=0, open=1, half-open=2)
- `arbitrage.websocket.reconnections`: Counter for connection stability

### 5. Resilience Patterns

**Circuit Breaker**:
- Three states: Closed (normal), Open (failing), HalfOpen (testing recovery)
- Configurable thresholds (5 failures → Open, 2 successes → Closed)
- Applied to: SNS publishing, external API calls

**Retry with Exponential Backoff + Jitter**:
- Exponential: `delay = baseDelay * 2^attempt`, capped at `maxDelay`
- Jitter: Randomize ±10% to prevent thundering herd
- Applied to: WebSocket reconnection, RPC calls, SNS publishing

**Rate Limiting** (Token Bucket):
- Binance: 1200 requests/minute with burst 50
- Prevents hitting API rate limits

**Layered Caching**:
- L1 (in-memory LRU): Sub-millisecond latency for hot data (pool state, gas prices)
- L2 (Redis): Persistent, cross-replica consistency
- Write-through strategy: writes go to both layers
- TTL: 10s for Binance orderbooks, 12s for Uniswap pool state (1 block)

## Quick Start (Docker Compose)

### Prerequisites

- **Go 1.21+**
- **Docker** and **Docker Compose**
- **Ethereum RPC Access**: Infura or Alchemy API keys (free tier works)

> **Note**: This setup uses Docker Compose for local development. For production deployment, consider Kubernetes, ECS, or other container orchestration platforms.

### Step 1: Clone and Configure

```bash
# Clone repository
git clone <repository-url>
cd cex-dex-arbitrage-bot

# Edit config.yaml and add your API keys
# Required: Replace YOUR_INFURA_KEY and YOUR_ALCHEMY_KEY
vim config.yaml
```

### Step 2: Start All Services

```bash
# Start ALL services including detector (Docker Compose)
# This builds Lambdas, builds detector image, and starts everything
make start

# Verify all services are healthy
make status

# Expected output:
# ✓ Redis: healthy
# ✓ LocalStack: healthy (with SNS, SQS, DynamoDB, Lambdas)
# ✓ Prometheus: healthy
# ✓ Jaeger: healthy
# ✓ Grafana: healthy
# ✓ Detector: healthy
```

### Step 3: Monitor

**Detector Logs**: View real-time arbitrage detection
```bash
make logs-detector
```

**Console Output**: Arbitrage opportunities will print formatted summaries

**Access URLs**:
- **Detector Health**: http://localhost:8080/health
- **Detector Metrics**: http://localhost:8080/metrics
- **Prometheus**: http://localhost:9090
  - Query: `arbitrage_opportunities_detected_total`
  - Query: `arbitrage_block_processing_duration_seconds`
- **Jaeger Traces**: http://localhost:16686
  - Service: `arbitrage-detector`
  - Operations: Price fetching, opportunity detection, SNS publishing
- **Grafana Dashboards**: http://localhost:3000
  - Login: admin/admin
  - Pre-configured datasources for Prometheus and Jaeger

## Development Commands

```bash
make help          # Show all available commands
make start         # Start ALL services including detector (Docker Compose)
make stop          # Stop all services
make clean         # Clean up containers and volumes
make restart       # Restart all services

make build         # Build detector binary (local)
make build-lambdas # Build Lambda functions (called by start)
make docker-build  # Build detector Docker image (called by start)

make test          # Run unit tests
make test-coverage # Run tests with coverage report

make logs          # Tail all service logs
make logs-detector # Tail detector logs only
make logs-localstack # Tail LocalStack logs
make shell-redis   # Open Redis CLI shell
make shell-localstack # Open LocalStack shell

make status        # Check health of all services + AWS resources
```

## Configuration Reference

See `config.example.yaml` for full configuration with comments.

**Key Settings**:
- `ethereum.websocket_urls`: Primary and fallback WebSocket endpoints
- `ethereum.rpc_endpoints`: HTTP RPC endpoints for contract calls
- `arbitrage.trade_sizes`: Trade sizes to test in wei (1 ETH = 1e18 wei)
- `arbitrage.min_profit_threshold`: Minimum profit percentage (0.5% = 0.5)
- `redis.address`: Redis server address
- `aws.endpoint`: LocalStack endpoint (remove for production AWS)

## Observability Deep Dive

### Metrics Exposed

**Endpoint**: http://localhost:9090/metrics

```prometheus
# Block processing
arbitrage_block_processing_duration_seconds_bucket{le="1"}
arbitrage_blocks_processed_total

# Opportunities
arbitrage_opportunities_detected_total{direction="CEX_TO_DEX",profitable="true"}
arbitrage_opportunities_profit_usd_bucket{le="100"}

# API Performance
arbitrage_cex_api_duration_seconds{exchange="binance",endpoint="orderbook"}
arbitrage_dex_quote_duration_seconds{dex="uniswapv3"}

# Infrastructure Health
arbitrage_websocket_reconnections_total
arbitrage_cache_hits_total{layer="L1"}
arbitrage_circuit_breaker_state{service="sns"}
```

### Distributed Tracing

**Trace Hierarchy**:
```
runDetector
├── Detect (block processing)
│   ├── detectForSize
│   │   ├── GetPrice (Binance) [parallel]
│   │   └── GetPrice (Uniswap) [parallel]
│   ├── CalculateProfit
│   └── PublishOpportunity
│       └── SNS Publish (with circuit breaker)
```

## Production Considerations

### Production: High Availability Considerations

**Current**: Single instance deployment (Docker Compose)
**Production Enhancement Options**:

**Option 1: Kubernetes with Leader Election**
- 2 replicas with active-standby pattern
- Leader election using Kubernetes Leases API or etcd
- Only active leader processes blocks
- Automatic failover in <5s on leader failure

**Option 2: Multi-Region Deployment**
- Deploy detector in multiple AWS regions
- Each region processes independently
- Regional RPC endpoints for low latency
- DynamoDB global tables for deduplication

**Option 3: Serverless (AWS Lambda)**
- EventBridge scheduled rules trigger Lambda
- Lambda pulls blocks and detects arbitrage
- Auto-scaling, no servers to manage
- Pay per execution model

### TODO: Chain Reorg Handling

Subscribe to `eth_subscribe("newHeads")` and detect reorgs by comparing parent hashes. Invalidate opportunities from reorg'd blocks.

### TODO: Multi-Region Deployment

Deploy detector pods in multiple AWS regions with regional RPC endpoints for redundancy.

### Security Best Practices

- **API Key Rotation**: Rotate Infura/Alchemy keys automatically
- **Private Key Management**: Use AWS KMS or HashiCorp Vault for wallet keys (if executing trades)
- **Network Segmentation**: Restrict egress traffic to only required endpoints (Binance, Infura, AWS services)
- **Secrets Management**: Use environment variables or AWS Secrets Manager for sensitive configuration

### Performance Optimizations

- **Connection Pooling**: Pool HTTP connections to Binance, reuse ethclient connections
- **Batch Processing**: Batch multiple trade sizes in single Uniswap multicall
- **Horizontal Scaling**: Run multiple detector instances for different trading pairs or strategies

## Testing Strategy

### Unit Tests
```bash
make test
```

- **arbitrage**: Mock PriceProviders, test detection logic
- **pricing**: Mock HTTP responses, test orderbook calculations
- **platform/cache**: Test cache hit/miss, eviction, TTL
- **platform/resilience**: Test circuit breaker state transitions, retry logic

### Integration Tests
```bash
make test-integration
```

- Use Docker Compose with DynamoDB Local
- Test end-to-end: mock blocks → detect arbitrage → verify SNS publish

## Trade-offs & Design Rationale

### 1. Block-Driven vs Event-Driven Pricing

**Choice**: Block-driven (requote on every block)

**Reasoning**:
- Atomic consistency between CEX and DEX snapshots
- No risk of missing Swap events during reconnection
- Simpler architecture, more predictable latency

**Trade-off**: Slightly higher RPC/API costs vs event-driven

### 2. In-Memory + Redis vs Redis Only

**Choice**: Layered cache (L1 memory + L2 Redis)

**Reasoning**:
- Sub-millisecond L1 hit latency for hot data
- Redis provides cross-replica consistency
- Reduces Redis load by 80-90%

**Trade-off**: Memory overhead per pod

### 3. SNS Fan-Out vs Direct Publishing

**Choice**: SNS fan-out to SQS

**Reasoning**:
- Decouples detector from consumers
- Easy to add new consumers (analytics, alerts, execution)
- Built-in retry and DLQ support

**Trade-off**: Added latency (~50-100ms)

## License

This is a coding challenge implementation demonstrating production-grade Go development.

## Contact

For questions or discussion, please open an issue.
