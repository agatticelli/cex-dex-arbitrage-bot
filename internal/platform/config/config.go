package config

import (
	"fmt"
	"math/big"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the arbitrage bot
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

// EthereumConfig holds Ethereum connection configuration
type EthereumConfig struct {
	WebSocketURLs []string        `mapstructure:"websocket_urls"`
	RPCEndpoints  []RPCEndpoint   `mapstructure:"rpc_endpoints"`
	Reconnect     ReconnectConfig `mapstructure:"reconnect"`
}

// RPCEndpoint represents an Ethereum RPC endpoint
type RPCEndpoint struct {
	URL    string `mapstructure:"url"`
	Weight int    `mapstructure:"weight"`
}

// ReconnectConfig holds WebSocket reconnection settings
type ReconnectConfig struct {
	MaxBackoff time.Duration `mapstructure:"max_backoff"`
	Jitter     float64       `mapstructure:"jitter"`
}

// ExchangesConfig holds configuration for all exchanges
type ExchangesConfig struct {
	Binance BinanceConfig `mapstructure:"binance"`
}

// BinanceConfig holds Binance-specific configuration
type BinanceConfig struct {
	BaseURL   string          `mapstructure:"base_url"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
	Burst             int `mapstructure:"burst"`
}

// UniswapConfig holds Uniswap V3 configuration
type UniswapConfig struct {
	QuoterAddress string   `mapstructure:"quoter_address"`
	PoolAddress   string   `mapstructure:"pool_address"` // Still used for cache key
	USDCAddress   string   `mapstructure:"usdc_address"`
	WETHAddress   string   `mapstructure:"weth_address"`
	FeeTiers      []uint32 `mapstructure:"fee_tiers"` // Fee tiers to try: [100, 500, 3000, 10000]
	RateLimit     struct {
		RequestsPerMinute int `mapstructure:"requests_per_minute"` // QuoterV2 rate limit
		Burst             int `mapstructure:"burst"`               // Burst size
	} `mapstructure:"rate_limit"`
}

// TradingPairConfig represents a fully parsed trading pair configuration
type TradingPairConfig struct {
	Name               string     // "ETH-USDC", "BTC-USDC", etc.
	Base               TokenInfo  // Base token (ETH, BTC, etc.)
	Quote              TokenInfo  // Quote token (USDC, USDT, etc.)
	TradeSizes         []string   // Optional trade size override
	MinProfitThreshold float64    // Optional min profit override
	parsedTradeSizes   []*big.Int // Parsed trade sizes
}

// GetParsedTradeSizes returns the parsed trade sizes for this pair
func (tpc *TradingPairConfig) GetParsedTradeSizes() []*big.Int {
	return tpc.parsedTradeSizes
}

// PairOverride holds optional per-pair configuration overrides
type PairOverride struct {
	TradeSizes         []string `mapstructure:"trade_sizes"`
	MinProfitThreshold float64  `mapstructure:"min_profit_threshold"`
}

// ArbitrageConfig holds arbitrage detection settings
type ArbitrageConfig struct {
	// Simple list of pairs: ["ETH-USDC", "BTC-USDC"]
	Pairs []string `mapstructure:"pairs"`

	// Optional per-pair overrides
	PairOverrides map[string]PairOverride `mapstructure:"pair_overrides"`

	// Global concurrency limit for DEX quotes (across all pairs)
	MaxConcurrentDEXQuotes int `mapstructure:"max_concurrent_dex_quotes"`

	// Global settings
	TradeSizes         []string `mapstructure:"trade_sizes"`
	MinProfitThreshold float64  `mapstructure:"min_profit_threshold"`

	// Internal parsed pairs
	parsedPairs []TradingPairConfig
}

// Parse converts simple pair names into full TradingPairConfig with registry lookup
func (a *ArbitrageConfig) Parse() error {
	// Reset parsed pairs to avoid duplicates on re-parse
	a.parsedPairs = nil

	if len(a.TradeSizes) == 0 {
		return fmt.Errorf("arbitrage.trade_sizes is required (human-readable base sizes)")
	}

	// Parse each pair
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

		// Apply pair-specific overrides if present
		if override, ok := a.PairOverrides[pairName]; ok {
			if len(override.TradeSizes) > 0 {
				cfg.TradeSizes = override.TradeSizes
			}
			if override.MinProfitThreshold > 0 {
				cfg.MinProfitThreshold = override.MinProfitThreshold
			}
		}

		// Parse trade sizes for this pair
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

// parseTradeSizeBase converts a human-readable base size into raw units using decimals.
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

// GetParsedPairs returns all parsed trading pair configurations
func (a *ArbitrageConfig) GetParsedPairs() []TradingPairConfig {
	return a.parsedPairs
}

// GetParsedTradeSizes returns the parsed trade sizes (legacy - for single pair)
// Deprecated: Use GetParsedPairs() instead for multi-pair support
func (ac *ArbitrageConfig) GetParsedTradeSizes() []*big.Int {
	if len(ac.parsedPairs) > 0 {
		return ac.parsedPairs[0].parsedTradeSizes
	}
	return nil
}

// GetTradeSizes returns parsed trade sizes as *big.Int (legacy - for single pair)
// Deprecated: Use GetParsedPairs() instead for multi-pair support
func (a *ArbitrageConfig) GetTradeSizes() []*big.Int {
	return a.GetParsedTradeSizes()
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// AWSConfig holds AWS service configuration
type AWSConfig struct {
	Endpoint    string `mapstructure:"endpoint"`
	Region      string `mapstructure:"region"`
	SNSTopicARN string `mapstructure:"sns_topic_arn"`
}

// CacheConfig holds caching configuration
type CacheConfig struct {
	L1MaxSize int           `mapstructure:"l1_max_size"`
	L2TTL     time.Duration `mapstructure:"l2_ttl"`
}

// ObservabilityConfig holds observability settings
type ObservabilityConfig struct {
	Logging LoggingConfig `mapstructure:"logging"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Tracing TracingConfig `mapstructure:"tracing"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // json or text
}

// MetricsConfig holds metrics settings
type MetricsConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Port    int  `mapstructure:"port"`
}

// TracingConfig holds tracing settings
type TracingConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
	Sampler  string `mapstructure:"sampler"` // always, never, ratio
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Port int `mapstructure:"port"`
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set config file path
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	// Read environment variables
	v.AutomaticEnv()

	// Set defaults
	setDefaults(v)

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		// Config file not found is not fatal if env vars are set
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Parse and validate
	if err := cfg.parse(); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// MustLoad loads configuration or panics
func MustLoad(configPath string) *Config {
	cfg, err := Load(configPath)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Ethereum defaults
	v.SetDefault("ethereum.reconnect.max_backoff", "30s")
	v.SetDefault("ethereum.reconnect.jitter", 0.2)

	// Binance defaults
	v.SetDefault("exchanges.binance.base_url", "https://api.binance.com")
	v.SetDefault("exchanges.binance.rate_limit.requests_per_minute", 1200)
	v.SetDefault("exchanges.binance.rate_limit.burst", 50)

	// Uniswap defaults
	v.SetDefault("uniswap.quoter_address", "0xb27308f9F90D607463bb33eA1BeBb41C27CE5AB6")
	v.SetDefault("uniswap.pool_address", "0x88e6A0c2dDD26FEEb64F039a2c41296FcB3f5640")

	// Arbitrage defaults
	v.SetDefault("arbitrage.pairs", []string{"ETH-USDC"}) // Default to single pair for backward compat
	v.SetDefault("arbitrage.max_concurrent_dex_quotes", 8)
	v.SetDefault("arbitrage.trade_sizes", []string{"1", "10", "100"})
	v.SetDefault("arbitrage.min_profit_threshold", 0.5)

	// Redis defaults
	v.SetDefault("redis.address", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	// AWS defaults
	v.SetDefault("aws.endpoint", "http://localhost:4566")
	v.SetDefault("aws.region", "us-east-1")
	v.SetDefault("aws.sns_topic_arn", "arn:aws:sns:us-east-1:000000000000:arbitrage-opportunities")

	// Cache defaults
	v.SetDefault("cache.l1_max_size", 1000)
	v.SetDefault("cache.l2_ttl", "60s")

	// Observability defaults
	v.SetDefault("observability.logging.level", "info")
	v.SetDefault("observability.logging.format", "json")
	v.SetDefault("observability.metrics.enabled", true)
	v.SetDefault("observability.metrics.port", 9091)
	v.SetDefault("observability.tracing.enabled", true)
	v.SetDefault("observability.tracing.endpoint", "localhost:4317")
	v.SetDefault("observability.tracing.sampler", "always")

	// HTTP defaults
	v.SetDefault("http.port", 8080)
}

// parse parses string values into their proper types
func (c *Config) parse() error {
	// Parse trading pairs (converts ["ETH-USDC"] to full TradingPairConfig with registry lookup)
	if err := c.Arbitrage.Parse(); err != nil {
		return fmt.Errorf("failed to parse arbitrage config: %w", err)
	}

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Ethereum validation
	if len(c.Ethereum.WebSocketURLs) == 0 {
		return fmt.Errorf("at least one WebSocket URL is required")
	}

	if len(c.Ethereum.RPCEndpoints) == 0 {
		return fmt.Errorf("at least one RPC endpoint is required")
	}

	// Arbitrage validation
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

	// Redis validation
	if c.Redis.Address == "" {
		return fmt.Errorf("redis address is required")
	}

	// AWS validation
	if c.AWS.Region == "" {
		return fmt.Errorf("AWS region is required")
	}

	if c.AWS.SNSTopicARN == "" {
		return fmt.Errorf("SNS topic ARN is required")
	}

	// Observability validation
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
