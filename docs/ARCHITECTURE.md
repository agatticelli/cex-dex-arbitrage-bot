# Architecture

This document describes the system architecture, design decisions, and data flow of the CEX-DEX Arbitrage Detection Bot.

## System Overview

```
+--------------------------------------------------------------------------------+
|                         CEX-DEX Arbitrage Detector                             |
+--------------------------------------------------------------------------------+
|                                                                                |
|  +------------------+                                                          |
|  |  Block Subscriber |<---- WebSocket: eth_subscribe("newHeads")               |
|  |   (blockchain/)   |<---- HTTP Fallback: eth_blockNumber polling             |
|  +--------+---------+                                                          |
|           |                                                                    |
|           | Block{Number, Hash, Timestamp}                                     |
|           v                                                                    |
|  +------------------+      +-----------------+                                 |
|  |     Detector     |<---->|  Price Pipeline |                                 |
|  |   (arbitrage/)   |      |  (concurrent)   |                                 |
|  +--------+---------+      +--------+--------+                                 |
|           |                         |                                          |
|           |                         +--> CEX Provider (Binance orderbook)      |
|           |                         |    +- Rate limited, circuit breaker      |
|           |                         |                                          |
|           |                         +--> DEX Provider (Uniswap QuoterV2)       |
|           |                              +- Multi-fee-tier, per-block cache    |
|           |                                                                    |
|           | Opportunity{Direction, ProfitPct, NetProfitUSD, ...}               |
|           v                                                                    |
|  +------------------+                                                          |
|  |    Publisher     |---> SNS Topic (optional) or NoOp (logging only)          |
|  |  (notification/) |                                                          |
|  +------------------+                                                          |
|                                                                                |
+--------------------------------------------------------------------------------+
|                            Platform Layer                                      |
|  +------------+  +-------------+  +-------------+  +---------------+           |
|  |   Cache    |  |  Resilience |  | Observability|  |    Config     |           |
|  | L1+L2/Redis|  | CB/Retry/RL |  | Metrics/Logs |  |  YAML+Env     |           |
|  +------------+  +-------------+  +-------------+  +---------------+           |
+--------------------------------------------------------------------------------+
```

## Package Structure

```
cex-dex-arbitrage-bot/
├── cmd/
│   └── detector/          # Main application entry point
│       └── main.go        # Wires components, starts block loop
│
├── internal/
│   ├── arbitrage/         # Core detection logic
│   │   ├── detector.go    # Per-pair detector orchestration
│   │   ├── calculator.go  # Profit calculation with fees & gas
│   │   ├── pipeline.go    # Concurrent price fetching pipeline
│   │   ├── opportunity.go # Opportunity data model
│   │   └── formatter.go   # Human-readable output formatting
│   │
│   ├── pricing/           # Price provider implementations
│   │   ├── binance.go     # CEX orderbook integration
│   │   ├── uniswap.go     # DEX QuoterV2 integration
│   │   ├── orderbook.go   # Orderbook data structures
│   │   ├── slippage.go    # Slippage calculation helpers
│   │   └── adapters.go    # Adapter pattern for unified interface
│   │
│   ├── blockchain/        # Ethereum connectivity
│   │   ├── subscriber.go  # Block subscription with reconnection
│   │   └── client_pool.go # HTTP RPC client pool for fallback
│   │
│   ├── notification/      # Alert publishing
│   │   ├── publisher.go   # SNS publisher implementation
│   │   └── noop.go        # No-op publisher for local development
│   │
│   ├── money/             # Fixed-point arithmetic
│   │   └── money.go       # Safe decimal operations
│   │
│   └── platform/          # Shared infrastructure
│       ├── config/        # Configuration loading
│       │   ├── config.go  # YAML + env vars
│       │   └── token_registry.go # Token metadata
│       │
│       ├── cache/         # Caching layer
│       │   ├── cache.go   # Interface definition
│       │   ├── memory.go  # In-memory L1 cache
│       │   ├── redis.go   # Redis L2 cache
│       │   ├── layered.go # L1+L2 composite cache
│       │   └── warming.go # Cache warmup utilities
│       │
│       ├── resilience/    # Fault tolerance
│       │   ├── circuit_breaker.go # Circuit breaker pattern
│       │   ├── retry.go           # Retry with backoff
│       │   └── rate_limiter.go    # Token bucket rate limiting
│       │
│       ├── observability/ # Monitoring
│       │   ├── logger.go  # Structured logging (slog)
│       │   ├── metrics.go # Prometheus metrics
│       │   └── tracer.go  # OpenTelemetry tracing
│       │
│       └── worker/        # Concurrency utilities
│           └── pool.go    # Worker pool for parallel tasks
│
└── docker-compose.yml     # Local development services
```

## Data Flow

### 1. Block Reception

```
Ethereum Node --WebSocket--> Subscriber --Channel--> Detector Loop
     |                           |
     |                           +- Validates block sequence
     |                           +- Detects gaps, triggers backfill
     |                           +- Falls back to HTTP polling if WS fails
     |
     +---HTTP (fallback)-------->
```

### 2. Price Fetching (per block)

```
Detector.Detect(block)
    |
    +--> Pipeline.Process() --parallel--> [size1, size2, size3]
    |                                         |
    |                                         +--> CEX.GetPrice(size, buy)
    |                                         +--> CEX.GetPrice(size, sell)
    |                                         +--> DEX.GetPrice(size, buy)   --> QuoterV2.quoteExactOutputSingle
    |                                         +--> DEX.GetPrice(size, sell)  --> QuoterV2.quoteExactInputSingle
    |
    +--> Calculator.CalculateProfit()
            |
            +- Gross profit = |DEX_out - CEX_out|
            +- Gas cost USD = gas_wei × gas_price × ETH_price
            +- Trading fees = CEX fee + DEX fee
            +- Net profit = Gross - Gas - Fees
```

### 3. Opportunity Detection

```
For each (size, direction):
    |
    +- CEX-->DEX: Buy on Binance, sell on Uniswap
    |   profit = DEX_sell_price - CEX_buy_price - costs
    |
    +- DEX-->CEX: Buy on Uniswap, sell on Binance
        profit = CEX_sell_price - DEX_buy_price - costs

If profit_pct >= min_threshold:
    +--> Publisher.PublishOpportunity(opp)
```

## SNS Integration (Optional)

The notification layer supports publishing opportunities to AWS SNS for downstream processing. When `sns_topic_arn` is configured, opportunities are published as JSON messages.

### Message Format

```json
{
  "opportunityId": "abc123",
  "blockNumber": 19500000,
  "timestamp": 1710000000,
  "direction": "CEX_TO_DEX",
  "tradingPair": "ETH-USDC",
  "cexPrice": "3500.00",
  "dexPrice": "3510.00",
  "netProfitUSD": "45.32",
  "profitPct": "1.23",
  "executionSteps": ["..."]
}
```

### Integration Patterns

With SNS as the notification backbone, you can build various downstream consumers:

```
                                    +-> SQS --> Lambda --> DynamoDB (persistence)
                                    |
Detector --> SNS Topic -------------+-> SQS --> Lambda --> Trading Service (execution)
                                    |
                                    +-> SQS --> Lambda --> Slack/Email (alerts)
                                    |
                                    +-> Kinesis Firehose --> S3 (analytics)
```

**Use Cases:**

1. **Persistence**: Subscribe an SQS queue to store opportunities in DynamoDB with TTL for historical analysis
2. **Trading Execution**: Route profitable opportunities to a trading service for automated execution
3. **Notifications**: Send alerts to Slack, email, or mobile push notifications
4. **Analytics**: Stream data to S3 via Kinesis Firehose for backtesting and strategy optimization

### Local Development

When `sns_topic_arn` is not configured (default), the bot uses `NoOpPublisher` which logs opportunities locally without any AWS dependencies. This makes local development simple:

```yaml
# config.yaml - SNS disabled (default)
aws:
  region: "us-east-1"
  # sns_topic_arn: ""  # Not set = NoOp publisher
```

## Key Design Decisions

### 1. Block-Driven Architecture

**Why:** The challenge specifies requoting on every block for atomic consistency.

- Ensures CEX and DEX prices are captured at the same logical time
- Avoids race conditions from event-driven approaches
- Simplifies reasoning about price freshness

**Implementation:** `blockchain.Subscriber` provides a channel of blocks. The detector processes each block synchronously before moving to the next.

### 2. Multi-Fee-Tier DEX Pricing

**Why:** Uniswap V3 has multiple fee tiers (0.01%, 0.05%, 0.3%, 1%). Different trades may get better execution in different pools.

**Implementation:** `UniswapProvider.GetPrice()` queries all configured fee tiers in parallel and returns the best execution price.

### 3. Layered Caching (L1 + L2)

**Why:**
- L1 (memory): Sub-millisecond access for hot data
- L2 (Redis): Persistence across restarts, shared state for horizontal scaling

**Implementation:** `LayeredCache` implements write-through caching with automatic L1 backfill on L2 hits.

### 4. Graceful Degradation

**Why:** External services (Infura, Binance) can fail. The system should continue operating with reduced functionality.

**Mechanisms:**
- Circuit breakers prevent cascade failures
- WebSocket to HTTP fallback for block subscription
- Cached prices used when fresh data unavailable
- Per-provider health tracking

### 5. Concurrent Price Fetching

**Why:** Fetching 4 prices per trade size sequentially would be too slow for block time (~12s).

**Implementation:** `Pipeline` uses `errgroup` to fetch CEX/DEX buy/sell prices in parallel for each trade size. A semaphore limits concurrent DEX calls to respect rate limits.

### 6. Pluggable Notification Layer

**Why:** The detector focuses on opportunity detection. What happens with opportunities (logging, persistence, trading, alerts) should be configurable.

**Implementation:** `NotificationPublisher` interface with two implementations:
- `Publisher`: Publishes to AWS SNS with resilience patterns
- `NoOpPublisher`: Logs locally for development/testing

## Resilience Patterns

### Circuit Breaker

Protects against failing services:

```
Closed --(5 failures)--> Open --(30s timeout)--> Half-Open
   ^                                                  |
   +------------(2 successes)-------------------------+
```

### Rate Limiting

Token bucket algorithm per provider:
- Binance: 1200 req/min (API limit)
- Infura: 60 req/min (free tier safe)

### Retry with Backoff

Exponential backoff with jitter for transient failures:
```
delay = min(base × 2^attempt, max_delay) × (1 ± jitter)
```

## Observability

### Metrics (Prometheus)

Key metrics exposed at `/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `arbitrage_opportunities_detected_total` | Counter | Opportunities found by pair/direction |
| `arbitrage_block_processing_duration_ms` | Histogram | Time to process each block |
| `arbitrage_cache_hits_total` | Counter | Cache hit rate by provider |
| `arbitrage_circuit_breaker_state` | Gauge | Circuit breaker state (0=closed, 1=open, 2=half-open) |
| `arbitrage_eth_price_usd` | Gauge | Current ETH price used for gas calculations |

### Structured Logging

Uses `log/slog` with JSON output:

```json
{
  "time": "2024-01-15T14:23:45Z",
  "level": "INFO",
  "msg": "profitable opportunity found",
  "opportunity_id": "abc123",
  "direction": "CEX_TO_DEX",
  "net_profit_usd": "45.32",
  "profit_pct": "1.23"
}
```

### Distributed Tracing

OpenTelemetry integration for request tracing across services (Jaeger UI at `:16686`).

## Scaling Considerations

### Current Architecture (Single Instance)

- Processes one block at a time
- Suitable for ~3-5 trading pairs
- Bounded by RPC rate limits

### Horizontal Scaling (Future)

To scale beyond single instance:

1. **Shard by trading pair**: Each instance handles different pairs
2. **Shared L2 cache**: Redis for cross-instance quote sharing
3. **Message queue**: Use SQS for backpressure between detector and consumers
4. **Leader election**: Single block subscriber, multiple processors

### Bottlenecks

1. **RPC rate limits**: Infura free tier limits QuoterV2 calls
2. **Block time**: ~12s bounds maximum throughput
3. **Price staleness**: CEX orderbook cached for 10s, DEX quotes for 15s

## Security Considerations

### Secrets Management

- API keys via environment variables (`ARB_*` prefix)
- No secrets in config files (use `.env` or secrets manager)
- AWS credentials via standard SDK chain (env vars, IAM roles, etc.)

### Network Security

- All external connections use TLS
- No outbound connections except configured endpoints
- Health endpoint (`/health`) requires no authentication

### Detection-Only Mode

- System does NOT execute trades
- No private keys or wallet access required
- Safe to run in read-only mode against mainnet
