# CEX-DEX Arbitrage Bot

Real-time arbitrage detection system that monitors price discrepancies between Binance (CEX) and Uniswap V3 (DEX) for ETH-based trading pairs.

## Features

- Multi-pair support via token registry (ETH-USDC, ETH-USDT, ETH-DAI)
- QuoterV2 integration for accurate DEX pricing
- Configurable multi-fee-tier optimization
- Per-block quote caching (CEX orderbook + DEX quotes)
- WebSocket block subscription with HTTP fallback and gap backfill
- Observability (Prometheus, Jaeger, Grafana dashboard)
- Resilience patterns (circuit breaker, retry, rate limiting)
- Optional SNS integration for downstream processing

## Architecture

```
+-----------------------------------------------------------------------+
|                      CEX-DEX Arbitrage Detector                       |
+-----------------------------------------------------------------------+
|                                                                       |
|  Block Subscriber --> Arbitrage Detector --> Notification Publisher  |
|     (WebSocket)          (per pair)              (SNS or NoOp)       |
|                              |                                        |
|                    +---------+---------+                              |
|                    v                   v                              |
|              Binance API         Uniswap QuoterV2                     |
|                    |                   |                              |
|                    +---------+---------+                              |
|                              v                                        |
|                    +-----------------------------+                    |
|                    |      Platform Layer         |                    |
|                    | Cache | Metrics | Resilience|                    |
|                    +-----------------------------+                    |
+-----------------------------------------------------------------------+
```

## Getting Started

### Prerequisites

- Go 1.25+
- Docker and Docker Compose
- Ethereum RPC access (Infura or Alchemy)

### Step 1: Clone and Configure

```bash
git clone <repository-url>
cd cex-dex-arbitrage-bot

# Copy example config
cp config.example.yaml config.yaml
```

### Step 2: Add Your API Keys

Edit `config.yaml` and replace the placeholder API keys:

```yaml
ethereum:
  websocket_urls:
    - "wss://mainnet.infura.io/ws/v3/YOUR_INFURA_KEY"
  rpc_endpoints:
    - url: "https://mainnet.infura.io/v3/YOUR_INFURA_KEY"
      weight: 1
```

You can get free API keys from:
- [Infura](https://infura.io) - 100k requests/day free
- [Alchemy](https://alchemy.com) - 300M compute units/month free

### Step 3: Start Services

```bash
# Start all services (Redis, Prometheus, Jaeger, Grafana, Detector)
make start

# Check that everything is running
make status

# View detector logs
make logs-detector
```

### Step 4: Access Dashboards

| Service | URL | Description |
|---------|-----|-------------|
| Health | http://localhost:8080/health | Detector health check |
| Metrics | http://localhost:8080/metrics | Prometheus metrics |
| Grafana | http://localhost:3000 | Dashboards (admin/admin) |
| Prometheus | http://localhost:9090 | Metrics queries |
| Jaeger | http://localhost:16686 | Distributed tracing |

### Step 5: Monitor Arbitrage Detection

1. Open Grafana at http://localhost:3000
2. Go to **Dashboards** → **Arbitrage Detector**
3. Watch for opportunities in the logs: `make logs-detector`

## Configuration

### Minimal Configuration

```yaml
ethereum:
  websocket_urls:
    - "wss://mainnet.infura.io/ws/v3/YOUR_KEY"
  rpc_endpoints:
    - url: "https://mainnet.infura.io/v3/YOUR_KEY"

arbitrage:
  pairs:
    - "ETH-USDC"
  trade_sizes:
    - "1"
    - "10"
  min_profit_threshold: 0.5  # 0.5%
```

### Supported Trading Pairs

Trading pairs use the format `BASE-QUOTE`. Currently only **ETH** is supported as the base token because:
- Gas costs are paid in ETH on Ethereum mainnet
- Profit calculations use ETH price for gas cost conversions

| Base | Quote Options |
|------|---------------|
| ETH | USDC, USDT, DAI |

Examples: `ETH-USDC`, `ETH-USDT`, `ETH-DAI`

### Environment Overrides

```bash
export ARB_ETHEREUM_WEBSOCKET_URLS="wss://mainnet.infura.io/ws/v3/KEY"
export ARB_ARBITRAGE_PAIRS="ETH-USDC,ETH-USDT"
export ARB_ARBITRAGE_TRADE_SIZES="1,10"
```

## SNS Integration (Optional)

The bot can publish detected opportunities to AWS SNS for downstream processing:

- **Persistence**: Store opportunities in DynamoDB
- **Trading execution**: Trigger automated trading
- **Notifications**: Send alerts via Slack, email
- **Analytics**: Feed data into analytics pipelines

### Configuration

```yaml
aws:
  region: "us-east-1"
  sns_topic_arn: "arn:aws:sns:us-east-1:123456789012:arbitrage-opportunities"
```

When `sns_topic_arn` is not configured, the bot uses a no-op publisher that only logs opportunities locally.

See [Architecture](docs/ARCHITECTURE.md) for SNS integration patterns.

## Package Structure

```
cex-dex-arbitrage-bot/
├── cmd/detector/         # Application entry point
├── internal/
│   ├── arbitrage/        # Detection logic
│   ├── pricing/          # CEX/DEX price providers
│   ├── blockchain/       # Ethereum integration
│   ├── notification/     # SNS publishing
│   ├── money/            # Fixed-point arithmetic
│   └── platform/         # Shared infrastructure
├── observability/
│   └── grafana/          # Grafana dashboards
└── docker-compose.yml
```

## Development

```bash
make help           # Show all commands
make build          # Build binary
make test           # Run tests
make test-coverage  # Coverage report

make start          # Start all services
make stop           # Stop services
make restart        # Restart detector only
make logs           # View all logs
make logs-detector  # View detector logs
```

## Key Metrics

Available in Grafana dashboard or via PromQL:

```promql
# Opportunities detected
arbitrage_opportunities_detected_total{pair, direction, profitable}

# Block processing latency (p95)
histogram_quantile(0.95, rate(arbitrage_block_processing_duration_bucket[5m]))

# Cache hit ratio
sum(rate(arbitrage_cache_hits_total[5m])) /
(sum(rate(arbitrage_cache_hits_total[5m])) + sum(rate(arbitrage_cache_misses_total[5m])))

# CEX/DEX API latency
histogram_quantile(0.95, rate(arbitrage_cex_api_duration_bucket[5m]))
histogram_quantile(0.95, rate(arbitrage_dex_quote_duration_bucket[5m]))
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - System design, data flow, and design decisions
- [API Reference](docs/API.md) - Key interfaces, types, and usage examples

## License

Coding challenge implementation.
