# Alerts (Prometheus)

## Critical

**RPC all unhealthy**
```
sum(arbitrage_rpc_endpoint_health == 1) == 0
```

**Uniswap Quoter error rate > 5%**
```
sum(rate(arbitrage_quoter_calls{status="error"}[5m])) 
/ 
sum(rate(arbitrage_quoter_calls[5m])) > 0.05
```

## Warning

**DEX quote cache hit ratio < 80%**
```
sum(rate(arbitrage_quote_cache_requests{status="hit"}[5m])) 
/ 
sum(rate(arbitrage_quote_cache_requests[5m])) < 0.80
```

**Frequent block gaps**
```
rate(arbitrage_block_gaps[5m]) > 0.1
```

**Binance API errors**
```
sum(rate(arbitrage_cex_api_calls{status="error"}[5m])) > 0
```
