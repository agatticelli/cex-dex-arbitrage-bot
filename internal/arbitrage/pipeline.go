package arbitrage

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
)

type Pipeline struct {
	cexProvider     PriceProvider
	dexProvider     PriceProvider
	analyzer        *opportunityAnalyzer
	logger          *observability.Logger
	metrics         *observability.Metrics
	tracer          observability.Tracer
	dexQuoteLimiter *semaphore.Weighted
	concurrency     int
	bufferSize      int
}

type PipelineConfig struct {
	CEXProvider     PriceProvider
	DEXProvider     PriceProvider
	Logger          *observability.Logger
	Metrics         *observability.Metrics
	Tracer          observability.Tracer
	DEXQuoteLimiter *semaphore.Weighted
	Concurrency     int
	BufferSize      int
	MinProfitPct    float64
}

type pipelineInput struct {
	ctx        context.Context
	blockNum   uint64
	blockTime  uint64
	tradeSize  *big.Int
	gasPrice   *big.Int
	ethPrice   float64
	pairName   string
	baseToken  tokenInfo
	quoteToken tokenInfo
}

type tokenInfo struct {
	symbol   string
	decimals int
}

type priceResult struct {
	input        pipelineInput
	cexBuyPrice  *pricing.Price
	cexSellPrice *pricing.Price
	dexBuyPrice  *pricing.Price
	dexSellPrice *pricing.Price
	cexBuyErr    error
	cexSellErr   error
	dexBuyErr    error
	dexSellErr   error
}

type opportunityAnalyzer struct {
	calculator   *Calculator
	logger       *observability.Logger
	minProfitPct float64
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 16
	}
	if cfg.MinProfitPct <= 0 {
		cfg.MinProfitPct = 0.5
	}
	if cfg.Tracer == nil {
		cfg.Tracer = observability.NewNoopTracer()
	}

	return &Pipeline{
		cexProvider:     cfg.CEXProvider,
		dexProvider:     cfg.DEXProvider,
		logger:          cfg.Logger,
		metrics:         cfg.Metrics,
		tracer:          cfg.Tracer,
		dexQuoteLimiter: cfg.DEXQuoteLimiter,
		concurrency:     cfg.Concurrency,
		bufferSize:      cfg.BufferSize,
		analyzer: &opportunityAnalyzer{
			calculator:   NewCalculator(),
			logger:       cfg.Logger,
			minProfitPct: cfg.MinProfitPct,
		},
	}
}

func (p *Pipeline) Process(
	ctx context.Context,
	blockNum uint64,
	blockTime uint64,
	tradeSizes []*big.Int,
	gasPrice *big.Int,
	ethPrice float64,
	pairName string,
	baseSymbol string,
	baseDecimals int,
	quoteSymbol string,
) []*Opportunity {
	if len(tradeSizes) == 0 {
		return nil
	}

	ctx, span := p.tracer.StartSpan(ctx, "Pipeline.Process",
		observability.WithAttributes(
			attribute.Int("concurrency", p.concurrency),
			attribute.Int("buffer_size", p.bufferSize),
			attribute.Int("trade_sizes_count", len(tradeSizes)),
			attribute.Int64("block_number", int64(blockNum)),
		),
	)
	defer span.End()

	inputCh := make(chan pipelineInput, p.bufferSize)
	priceCh := make(chan priceResult, p.bufferSize)
	outputCh := make(chan *Opportunity, p.bufferSize*2)

	var priceWg sync.WaitGroup
	var analysisWg sync.WaitGroup

	for i := 0; i < p.concurrency; i++ {
		priceWg.Add(1)
		go func() {
			defer priceWg.Done()
			p.priceFetchStage(ctx, inputCh, priceCh)
		}()
	}

	go func() {
		priceWg.Wait()
		close(priceCh)
	}()

	analysisWg.Add(1)
	go func() {
		defer analysisWg.Done()
		p.analysisStage(ctx, priceCh, outputCh)
	}()

	go func() {
		analysisWg.Wait()
		close(outputCh)
	}()

	for _, size := range tradeSizes {
		select {
		case <-ctx.Done():
			close(inputCh)
			return nil
		case inputCh <- pipelineInput{
			ctx:        ctx,
			blockNum:   blockNum,
			blockTime:  blockTime,
			tradeSize:  size,
			gasPrice:   gasPrice,
			ethPrice:   ethPrice,
			pairName:   pairName,
			baseToken:  tokenInfo{symbol: baseSymbol, decimals: baseDecimals},
			quoteToken: tokenInfo{symbol: quoteSymbol},
		}:
		}
	}
	close(inputCh)

	var opportunities []*Opportunity
	for opp := range outputCh {
		if opp != nil {
			opportunities = append(opportunities, opp)
		}
	}

	return opportunities
}

func (p *Pipeline) priceFetchStage(ctx context.Context, input <-chan pipelineInput, output chan<- priceResult) {
	for in := range input {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stageStart := time.Now()
		result := priceResult{input: in}

		var wg sync.WaitGroup
		wg.Add(4)

		go func() {
			defer wg.Done()
			fetchCtx, span := p.tracer.StartSpan(
				in.ctx,
				"CEX.GetPrice",
				observability.WithAttributes(
					attribute.String("side", "buy"),
					attribute.String("pair", in.pairName),
					attribute.String("trade_size", in.tradeSize.String()),
					attribute.Int64("block_number", int64(in.blockNum)),
				),
			)
			defer span.End()
			price, err := p.cexProvider.GetPrice(fetchCtx, in.tradeSize, true, nil, in.blockNum)
			if err != nil {
				span.NoticeError(err)
			}
			result.cexBuyPrice = price
			result.cexBuyErr = err
		}()

		go func() {
			defer wg.Done()
			fetchCtx, span := p.tracer.StartSpan(
				in.ctx,
				"CEX.GetPrice",
				observability.WithAttributes(
					attribute.String("side", "sell"),
					attribute.String("pair", in.pairName),
					attribute.String("trade_size", in.tradeSize.String()),
					attribute.Int64("block_number", int64(in.blockNum)),
				),
			)
			defer span.End()
			price, err := p.cexProvider.GetPrice(fetchCtx, in.tradeSize, false, nil, in.blockNum)
			if err != nil {
				span.NoticeError(err)
			}
			result.cexSellPrice = price
			result.cexSellErr = err
		}()

		go func() {
			defer wg.Done()
			fetchCtx, span := p.tracer.StartSpan(
				in.ctx,
				"DEX.GetPrice",
				observability.WithAttributes(
					attribute.String("side", "buy"),
					attribute.String("pair", in.pairName),
					attribute.String("trade_size", in.tradeSize.String()),
					attribute.Int64("block_number", int64(in.blockNum)),
				),
			)
			defer span.End()
			if p.dexQuoteLimiter != nil {
				if err := p.dexQuoteLimiter.Acquire(fetchCtx, 1); err != nil {
					span.NoticeError(err)
					result.dexBuyErr = err
					return
				}
				defer p.dexQuoteLimiter.Release(1)
			}
			price, err := p.dexProvider.GetPrice(fetchCtx, in.tradeSize, true, in.gasPrice, in.blockNum)
			if err != nil {
				span.NoticeError(err)
			}
			result.dexBuyPrice = price
			result.dexBuyErr = err
		}()

		go func() {
			defer wg.Done()
			fetchCtx, span := p.tracer.StartSpan(
				in.ctx,
				"DEX.GetPrice",
				observability.WithAttributes(
					attribute.String("side", "sell"),
					attribute.String("pair", in.pairName),
					attribute.String("trade_size", in.tradeSize.String()),
					attribute.Int64("block_number", int64(in.blockNum)),
				),
			)
			defer span.End()
			if p.dexQuoteLimiter != nil {
				if err := p.dexQuoteLimiter.Acquire(fetchCtx, 1); err != nil {
					span.NoticeError(err)
					result.dexSellErr = err
					return
				}
				defer p.dexQuoteLimiter.Release(1)
			}
			price, err := p.dexProvider.GetPrice(fetchCtx, in.tradeSize, false, in.gasPrice, in.blockNum)
			if err != nil {
				span.NoticeError(err)
			}
			result.dexSellPrice = price
			result.dexSellErr = err
		}()

		wg.Wait()

		if p.metrics != nil {
			p.metrics.RecordPipelineStageLatency(ctx, "price_fetch", time.Since(stageStart))
			p.metrics.RecordPipelineItemProcessed(ctx, "price_fetch")
		}

		select {
		case <-ctx.Done():
			return
		case output <- result:
		}
	}
}

func (p *Pipeline) analysisStage(ctx context.Context, input <-chan priceResult, output chan<- *Opportunity) {
	for result := range input {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stageStart := time.Now()

		if result.dexBuyPrice == nil || result.dexSellPrice == nil {
			if p.logger != nil {
				if result.dexBuyErr != nil {
					p.logger.Debug("skipping due to DEX buy error", "error", result.dexBuyErr)
				}
				if result.dexSellErr != nil {
					p.logger.Debug("skipping due to DEX sell error", "error", result.dexSellErr)
				}
			}
			continue
		}

		if result.cexBuyPrice != nil {
			if opp := p.analyzer.analyze(
				result.input.ctx,
				result.input.blockNum,
				result.input.tradeSize,
				CEXToDEX,
				result.cexBuyPrice,
				result.dexSellPrice,
				result.input.ethPrice,
				result.input.blockTime,
				result.input.pairName,
				result.input.baseToken.symbol,
				result.input.quoteToken.symbol,
				result.input.baseToken.decimals,
			); opp != nil {
				select {
				case <-ctx.Done():
					return
				case output <- opp:
				}
			}
		}

		if result.cexSellPrice != nil {
			if opp := p.analyzer.analyze(
				result.input.ctx,
				result.input.blockNum,
				result.input.tradeSize,
				DEXToCEX,
				result.dexBuyPrice,
				result.cexSellPrice,
				result.input.ethPrice,
				result.input.blockTime,
				result.input.pairName,
				result.input.baseToken.symbol,
				result.input.quoteToken.symbol,
				result.input.baseToken.decimals,
			); opp != nil {
				select {
				case <-ctx.Done():
					return
				case output <- opp:
				}
			}
		}

		if p.metrics != nil {
			p.metrics.RecordPipelineStageLatency(ctx, "analysis", time.Since(stageStart))
			p.metrics.RecordPipelineItemProcessed(ctx, "analysis")
		}
	}
}

func (a *opportunityAnalyzer) analyze(
	ctx context.Context,
	blockNum uint64,
	tradeSize *big.Int,
	direction Direction,
	buyPrice, sellPrice *pricing.Price,
	ethPriceUSD float64,
	blockTime uint64,
	pairName, baseSymbol, quoteSymbol string,
	baseDecimals int,
) *Opportunity {
	var cexPrice, dexPrice *pricing.Price
	if direction == CEXToDEX {
		cexPrice = buyPrice
		dexPrice = sellPrice
	} else {
		cexPrice = sellPrice
		dexPrice = buyPrice
	}

	gasCostUSD := calculateGasCostUSD(dexPrice.GasCost, ethPriceUSD)

	profitMetrics, err := a.calculator.CalculateProfit(direction, tradeSize, cexPrice, dexPrice, gasCostUSD)
	if err != nil {
		if a.logger != nil {
			a.logger.LogError(ctx, "failed to calculate profit", err,
				"direction", direction.String(),
				"trade_size", tradeSize.String(),
			)
		}
		return nil
	}

	opp := NewOpportunity(blockNum, direction, tradeSize)
	if blockTime > 0 {
		opp.Timestamp = int64(blockTime)
	}
	opp.TradingPair = pairName
	opp.SetBaseInfo(baseSymbol, quoteSymbol, baseDecimals)
	opp.SetPrices(cexPrice.Value, dexPrice.Value)
	opp.SetProfitMetrics(
		profitMetrics.GrossProfitUSD,
		profitMetrics.NetProfitUSD,
		profitMetrics.ProfitPct,
	)

	var gasCost *big.Int
	if direction == CEXToDEX {
		gasCost = sellPrice.GasCost
	} else {
		gasCost = buyPrice.GasCost
	}
	if gasCost == nil {
		gasCost = big.NewInt(0)
	}
	opp.SetCosts(gasCost, profitMetrics.GasCostUSD, profitMetrics.TradingFeesUSD)
	a.addExecutionAndRisk(opp, direction, buyPrice, sellPrice, gasCost, profitMetrics)

	return opp
}

func (p *Pipeline) Close() {
	// Pipeline workers are scoped per Process call and exit when channels close.
}

func (a *opportunityAnalyzer) addExecutionAndRisk(
	opp *Opportunity,
	direction Direction,
	buyPrice *pricing.Price,
	sellPrice *pricing.Price,
	gasCost *big.Int,
	profitMetrics *ProfitMetrics,
) {
	if direction == CEXToDEX {
		opp.AddExecutionStep(fmt.Sprintf("Buy %s %s on Binance at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, buyPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Transfer %s to wallet (if needed)", opp.BaseSymbol))
		opp.AddExecutionStep(fmt.Sprintf("Sell %s %s on Uniswap at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, sellPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Pay gas cost: %s wei", gasCost.String()))
	} else {
		opp.AddExecutionStep(fmt.Sprintf("Buy %s %s on Uniswap at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, buyPrice.Value.Text('f', 2)))
		opp.AddExecutionStep(fmt.Sprintf("Pay gas cost: %s wei", gasCost.String()))
		opp.AddExecutionStep(fmt.Sprintf("Transfer %s to Binance (if needed)", opp.BaseSymbol))
		opp.AddExecutionStep(fmt.Sprintf("Sell %s %s on Binance at $%s", formatBigFloat(opp.TradeSizeBase, 4), opp.BaseSymbol, sellPrice.Value.Text('f', 2)))
	}

	if buyPrice.Slippage.Cmp(big.NewFloat(0.5)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High buy slippage: %s%%", buyPrice.Slippage.Text('f', 2)))
	}
	if sellPrice.Slippage.Cmp(big.NewFloat(0.5)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High sell slippage: %s%%", sellPrice.Slippage.Text('f', 2)))
	}
	if profitMetrics.GasCostUSD.Cmp(big.NewFloat(50)) > 0 {
		opp.AddRiskFactor(fmt.Sprintf("High gas cost: $%s", profitMetrics.GasCostUSD.Text('f', 2)))
	}
	opp.AddRiskFactor("Requires cross-exchange transfer (time risk)")
}

func calculateGasCostUSD(gasCostWei *big.Int, gasTokenPriceUSD float64) *big.Float {
	if gasCostWei == nil || gasCostWei.Cmp(big.NewInt(0)) == 0 {
		return big.NewFloat(0)
	}

	gasCostToken := new(big.Float).SetInt(gasCostWei)
	gasCostToken.Quo(gasCostToken, big.NewFloat(1e18))

	return new(big.Float).Mul(gasCostToken, big.NewFloat(gasTokenPriceUSD))
}
