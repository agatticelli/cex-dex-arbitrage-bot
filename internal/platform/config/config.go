package config

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Ethereum      EthereumConfig      `mapstructure:"ethereum"`
	Exchanges     ExchangesConfig     `mapstructure:"exchanges"`
	Uniswap       UniswapConfig       `mapstructure:"uniswap"`
	Arbitrage     ArbitrageConfig     `mapstructure:"arbitrage"`
	Redis         RedisConfig         `mapstructure:"redis"`
	AWS           AWSConfig           `mapstructure:"aws"`
	Cache         CacheConfig         `mapstructure:"cache"`
	Observability ObservabilityConfig `mapstructure:"observability"`
	HTTP          HTTPConfig          `mapstructure:"http"`
}

type EthereumConfig struct {
	WebSocketURLs []string        `mapstructure:"websocket_urls"`
	RPCEndpoints  []RPCEndpoint   `mapstructure:"rpc_endpoints"`
	Reconnect     ReconnectConfig `mapstructure:"reconnect"`
}

type RPCEndpoint struct {
	URL    string `mapstructure:"url"`
	Weight int    `mapstructure:"weight"`
}

type ReconnectConfig struct {
	MaxBackoff time.Duration `mapstructure:"max_backoff"`
	Jitter     float64       `mapstructure:"jitter"`
}

type ExchangesConfig struct {
	Binance BinanceConfig `mapstructure:"binance"`
}

type BinanceConfig struct {
	BaseURL   string          `mapstructure:"base_url"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
	Burst             int `mapstructure:"burst"`
}

type UniswapConfig struct {
	QuoterAddress string   `mapstructure:"quoter_address"`
	PoolAddress   string   `mapstructure:"pool_address"`
	USDCAddress   string   `mapstructure:"usdc_address"`
	WETHAddress   string   `mapstructure:"weth_address"`
	FeeTiers      []uint32 `mapstructure:"fee_tiers"`
	RateLimit     struct {
		RequestsPerMinute int `mapstructure:"requests_per_minute"`
		Burst             int `mapstructure:"burst"`
	} `mapstructure:"rate_limit"`
}

type TradingPairConfig struct {
	Name               string
	Base               TokenInfo
	Quote              TokenInfo
	TradeSizes         []string
	MinProfitThreshold float64
	parsedTradeSizes   []*big.Int
}

func (tpc *TradingPairConfig) GetParsedTradeSizes() []*big.Int {
	return tpc.parsedTradeSizes
}

type PairOverride struct {
	TradeSizes         []string `mapstructure:"trade_sizes"`
	MinProfitThreshold float64  `mapstructure:"min_profit_threshold"`
}

type CalculatorValidationConfig struct {
	MinExpectedProfitUSD float64 `mapstructure:"min_expected_profit_usd"`
	MinAbsDiffUSD        float64 `mapstructure:"min_abs_diff_usd"`
	MaxRelDiffTolerance  float64 `mapstructure:"max_rel_diff_tolerance"`
}

type GasPriceConfig struct {
	TTL                 time.Duration `mapstructure:"ttl"`
	VolatileTTL         time.Duration `mapstructure:"volatile_ttl"`
	VolatilityThreshold float64       `mapstructure:"volatility_threshold"`
}

type ArbitrageConfig struct {
	Pairs                  []string                   `mapstructure:"pairs"`
	PairOverrides          map[string]PairOverride    `mapstructure:"pair_overrides"`
	MaxConcurrentDEXQuotes int                        `mapstructure:"max_concurrent_dex_quotes"`
	TradeSizes             []string                   `mapstructure:"trade_sizes"`
	MinProfitThreshold     float64                    `mapstructure:"min_profit_threshold"`
	CalculatorValidation   CalculatorValidationConfig `mapstructure:"calculator_validation"`
	GasPrice               GasPriceConfig             `mapstructure:"gas_price"`
	parsedPairs            []TradingPairConfig
}

func (a *ArbitrageConfig) Parse() error {
	a.parsedPairs = nil

	if len(a.TradeSizes) == 0 {
		return fmt.Errorf("arbitrage.trade_sizes is required (human-readable base sizes)")
	}

	for _, pairName := range a.Pairs {
		base, quote, err := ParsePair(pairName)
		if err != nil {
			return fmt.Errorf("failed to parse pair %s: %w", pairName, err)
		}

		cfg := TradingPairConfig{
			Name:               pairName,
			Base:               base,
			Quote:              quote,
			TradeSizes:         a.TradeSizes,
			MinProfitThreshold: a.MinProfitThreshold,
		}

		if override, ok := a.PairOverrides[pairName]; ok {
			if len(override.TradeSizes) > 0 {
				cfg.TradeSizes = override.TradeSizes
			}
			if override.MinProfitThreshold > 0 {
				cfg.MinProfitThreshold = override.MinProfitThreshold
			}
		}

		tradeSizes := make([]*big.Int, 0, len(cfg.TradeSizes))
		for _, sizeStr := range cfg.TradeSizes {
			size, err := parseTradeSizeBase(sizeStr, base.Decimals)
			if err != nil {
				return fmt.Errorf("invalid trade size for pair %s: %w", pairName, err)
			}
			tradeSizes = append(tradeSizes, size)
		}
		cfg.parsedTradeSizes = tradeSizes

		a.parsedPairs = append(a.parsedPairs, cfg)
	}

	return nil
}

func parseTradeSizeBase(sizeStr string, decimals int) (*big.Int, error) {
	if decimals < 0 {
		decimals = 0
	}
	val, _, err := big.ParseFloat(sizeStr, 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, fmt.Errorf("invalid base trade size: %s", sizeStr)
	}
	if val.Sign() <= 0 {
		return nil, fmt.Errorf("trade size must be positive: %s", sizeStr)
	}
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	val.Mul(val, scale)
	raw := new(big.Int)
	val.Int(raw)
	if raw.Sign() <= 0 {
		return nil, fmt.Errorf("trade size too small after scaling: %s", sizeStr)
	}
	return raw, nil
}

func (a *ArbitrageConfig) GetParsedPairs() []TradingPairConfig {
	return a.parsedPairs
}

func (ac *ArbitrageConfig) GetParsedTradeSizes() []*big.Int {
	if len(ac.parsedPairs) > 0 {
		return ac.parsedPairs[0].parsedTradeSizes
	}
	return nil
}

func (a *ArbitrageConfig) GetTradeSizes() []*big.Int {
	return a.GetParsedTradeSizes()
}

type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AWSConfig struct {
	Region      string `mapstructure:"region"`
	SNSTopicARN string `mapstructure:"sns_topic_arn"`
}

func (a *AWSConfig) SNSEnabled() bool {
	return a.SNSTopicARN != ""
}

type CacheConfig struct {
	L1MaxSize int           `mapstructure:"l1_max_size"`
	L2TTL     time.Duration `mapstructure:"l2_ttl"`
}

type ObservabilityConfig struct {
	Logging LoggingConfig `mapstructure:"logging"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Tracing TracingConfig `mapstructure:"tracing"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type MetricsConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type TracingConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
	Sampler  string `mapstructure:"sampler"`
}

type HTTPConfig struct {
	Port int `mapstructure:"port"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	v.SetEnvPrefix("ARB")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	applyEnvOverrides(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := cfg.parse(); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

func MustLoad(configPath string) *Config {
	cfg, err := Load(configPath)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("ethereum.reconnect.max_backoff", "30s")
	v.SetDefault("ethereum.reconnect.jitter", 0.2)

	v.SetDefault("exchanges.binance.base_url", "https://api.binance.com")
	v.SetDefault("exchanges.binance.rate_limit.requests_per_minute", 1200)
	v.SetDefault("exchanges.binance.rate_limit.burst", 50)

	v.SetDefault("uniswap.quoter_address", "0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	v.SetDefault("uniswap.pool_address", "0x88e6A0c2dDD26FEEb64F039a2c41296FcB3f5640")

	v.SetDefault("arbitrage.pairs", []string{"ETH-USDC"})
	v.SetDefault("arbitrage.max_concurrent_dex_quotes", 8)
	v.SetDefault("arbitrage.trade_sizes", []string{"1", "10", "100"})
	v.SetDefault("arbitrage.min_profit_threshold", 0.5)

	v.SetDefault("arbitrage.gas_price.ttl", "12s")
	v.SetDefault("arbitrage.gas_price.volatile_ttl", "3s")
	v.SetDefault("arbitrage.gas_price.volatility_threshold", 0.20)

	v.SetDefault("redis.address", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	v.SetDefault("aws.region", "us-east-1")
	v.SetDefault("aws.sns_topic_arn", "")

	v.SetDefault("cache.l1_max_size", 1000)
	v.SetDefault("cache.l2_ttl", "60s")

	v.SetDefault("observability.logging.level", "info")
	v.SetDefault("observability.logging.format", "json")
	v.SetDefault("observability.metrics.enabled", true)
	v.SetDefault("observability.tracing.enabled", true)
	v.SetDefault("observability.tracing.endpoint", "localhost:4317")
	v.SetDefault("observability.tracing.sampler", "always")

	v.SetDefault("http.port", 8080)
}

func applyEnvOverrides(v *viper.Viper) {
	if urls := os.Getenv("ARB_ETHEREUM_WEBSOCKET_URLS"); urls != "" {
		v.Set("ethereum.websocket_urls", splitCSV(urls))
	}
	if endpoints := os.Getenv("ARB_ETHEREUM_RPC_ENDPOINTS"); endpoints != "" {
		v.Set("ethereum.rpc_endpoints", parseRPCEndpoints(endpoints))
	}
	if pairs := os.Getenv("ARB_ARBITRAGE_PAIRS"); pairs != "" {
		v.Set("arbitrage.pairs", splitCSV(pairs))
	}
	if sizes := os.Getenv("ARB_ARBITRAGE_TRADE_SIZES"); sizes != "" {
		v.Set("arbitrage.trade_sizes", splitCSV(sizes))
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseRPCEndpoints(value string) []map[string]interface{} {
	parts := splitCSV(value)
	out := make([]map[string]interface{}, 0, len(parts))
	for _, p := range parts {
		url := p
		weight := 1
		if strings.Contains(p, "|") {
			segments := strings.SplitN(p, "|", 2)
			url = strings.TrimSpace(segments[0])
			if w, err := strconv.Atoi(strings.TrimSpace(segments[1])); err == nil && w > 0 {
				weight = w
			}
		}
		if url == "" {
			continue
		}
		out = append(out, map[string]interface{}{
			"url":    url,
			"weight": weight,
		})
	}
	return out
}

func (c *Config) parse() error {
	if err := c.Arbitrage.Parse(); err != nil {
		return fmt.Errorf("failed to parse arbitrage config: %w", err)
	}
	return nil
}

func (c *Config) Validate() error {
	if len(c.Ethereum.WebSocketURLs) == 0 {
		return fmt.Errorf("at least one WebSocket URL is required")
	}

	if len(c.Ethereum.RPCEndpoints) == 0 {
		return fmt.Errorf("at least one RPC endpoint is required")
	}

	if len(c.Arbitrage.parsedPairs) == 0 {
		return fmt.Errorf("at least one trading pair is required")
	}

	for _, pair := range c.Arbitrage.parsedPairs {
		if len(pair.parsedTradeSizes) == 0 {
			return fmt.Errorf("at least one trade size is required for pair %s", pair.Name)
		}
		if pair.MinProfitThreshold < 0 {
			return fmt.Errorf("min profit threshold must be >= 0 for pair %s", pair.Name)
		}
		if !pair.Quote.IsStablecoin {
			return fmt.Errorf("quote token must be a stablecoin for accurate USD profit metrics: pair %s (quote=%s)", pair.Name, pair.Quote.Symbol)
		}
	}

	if c.Redis.Address == "" {
		return fmt.Errorf("redis address is required")
	}

	if c.AWS.SNSEnabled() && c.AWS.Region == "" {
		return fmt.Errorf("AWS region is required when SNS is configured")
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.Observability.Logging.Level] {
		return fmt.Errorf("invalid log level: %s", c.Observability.Logging.Level)
	}

	validLogFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validLogFormats[c.Observability.Logging.Format] {
		return fmt.Errorf("invalid log format: %s", c.Observability.Logging.Format)
	}

	return nil
}
