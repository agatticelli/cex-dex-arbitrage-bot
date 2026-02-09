# Runbook

This runbook covers common operational issues and how to diagnose them.

## Health Endpoints

- `GET /health`: returns status for RPC, CEX (Binance), and DEX (Uniswap per pair).
- `GET /ready`: returns `200` only when all critical dependencies are healthy.

## Common Incidents

### 1) RPC Endpoints Unhealthy
**Symptoms**
- `/health` shows `rpc.status=unhealthy`
- Logs show repeated `RPC endpoint health check failed`

**Actions**
1. Check `/health` to see which endpoints are unhealthy.
2. Verify RPC provider status in their dashboards.
3. Rotate to a backup provider or add more RPC endpoints.

### 2) Binance API Errors / Rate Limits
**Symptoms**
- `/health` shows `cex.status=degraded` or `unhealthy`
- Elevated `arbitrage.cex.api.calls{status="error"}`

**Actions**
1. Check if rate limits are exceeded; reduce trading pairs or trade sizes.
2. Increase rate limits in config (if your plan allows).
3. Verify network connectivity to `api.binance.com`.

### 3) Uniswap Quoter Failures
**Symptoms**
- `/health` shows `dex.<pair>.status=degraded` or `unhealthy`
- Quoter error rate high

**Actions**
1. Check RPC provider health and latency.
2. Reduce `max_concurrent_dex_quotes`.
3. Verify QuoterV2 contract address and RPC permissions.

### 4) Cache Hit Ratio Drops
**Symptoms**
- Cache hit ratio < 80% for `quote_cache_requests`

**Actions**
1. Verify Redis availability (if using L2).
2. Ensure block number is stable and not causing cache churn.
3. Reduce number of fee tiers / trade sizes if necessary.

## Diagnostic Commands

```bash
curl localhost:8080/health
curl localhost:8080/ready
curl localhost:8080/metrics | grep quote_cache_requests
```
