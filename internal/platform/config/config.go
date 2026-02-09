package config

import (
	"fmt"
	"math/big"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the arbitrage bot
type Config struct {
	Ethereum     EthereumConfig     `mapstructure:"ethereum"`
	Exchanges    ExchangesConfig    `mapstructure:"exchanges"`
	Uniswap      UniswapConfig      `mapstructure:"uniswap"`
	Arbitrage    ArbitrageConfig    `mapstructure:"arbitrage"`
	Redis        RedisConfig        `mapstructure:"redis"`
	AWS          AWSConfig          `mapstructure:"aws"`
	Cache        CacheConfig        `mapstructure:"cache"`
	Observability ObservabilityConfig `mapstructure:"observability"`
	HTTP         HTTPConfig         `mapstructure:"http"`
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
	FeeTiers      []uint32 `mapstructure:"fee_tiers"` // Try all standard Uniswap V3 tiers: [100, 500, 3000, 10000]
}

// ArbitrageConfig holds arbitrage detection settings
type ArbitrageConfig struct {
	TradeSizes          []string `mapstructure:"trade_sizes"` // Wei strings
	MinProfitThreshold  float64  `mapstructure:"min_profit_threshold"`
	parsedTradeSizes    []*big.Int
}

// GetParsedTradeSizes returns the parsed trade sizes
func (ac *ArbitrageConfig) GetParsedTradeSizes() []*big.Int {
	return ac.parsedTradeSizes
}

// GetTradeSizes returns parsed trade sizes as *big.Int
func (a *ArbitrageConfig) GetTradeSizes() []*big.Int {
	return a.parsedTradeSizes
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
	v.SetDefault("arbitrage.trade_sizes", []string{
		"1000000000000000000",    // 1 ETH
		"10000000000000000000",   // 10 ETH
		"100000000000000000000",  // 100 ETH
	})
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
	// Parse trade sizes from string to *big.Int
	tradeSizes := make([]*big.Int, 0, len(c.Arbitrage.TradeSizes))
	for _, sizeStr := range c.Arbitrage.TradeSizes {
		size := new(big.Int)
		if _, ok := size.SetString(sizeStr, 10); !ok {
			return fmt.Errorf("invalid trade size: %s", sizeStr)
		}
		tradeSizes = append(tradeSizes, size)
	}
	c.Arbitrage.parsedTradeSizes = tradeSizes

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
	if len(c.Arbitrage.parsedTradeSizes) == 0 {
		return fmt.Errorf("at least one trade size is required")
	}

	if c.Arbitrage.MinProfitThreshold < 0 {
		return fmt.Errorf("min profit threshold must be >= 0")
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
