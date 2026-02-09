package blockchain

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/resilience"
)

// Block represents an Ethereum block header
type Block struct {
	Number    *big.Int
	Hash      common.Hash
	Timestamp uint64
	ParentHash common.Hash
}

// IsValid checks if block is valid
func (b *Block) IsValid() bool {
	return b.Number != nil && b.Number.Uint64() > 0
}

// Subscriber manages Ethereum block subscriptions with automatic reconnection
type Subscriber struct {
	wsURLs             []string
	currentURLIdx      int
	client             *ethclient.Client
	subscription       interface{}
	logger             *observability.Logger
	metrics            *observability.Metrics
	lastBlockNumber    uint64
	reconnectConfig    ReconnectConfig
	mu                 sync.RWMutex
	isConnected        bool
	heartbeatInterval  time.Duration
	heartbeatTimeout   time.Duration
	messageTimeout     time.Duration
	reconnectAttempts  int
}

// SubscriberConfig holds subscriber configuration
type SubscriberConfig struct {
	WebSocketURLs     []string
	Logger            *observability.Logger
	Metrics           *observability.Metrics
	ReconnectConfig   ReconnectConfig
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	MessageTimeout    time.Duration
}

// ReconnectConfig holds reconnection configuration
type ReconnectConfig struct {
	MaxBackoff time.Duration
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Jitter     float64
}

// DefaultReconnectConfig returns default reconnection configuration
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		MaxBackoff: 30 * time.Second,
		BaseDelay:  1 * time.Second,
		MaxDelay:   30 * time.Second,
		Jitter:     0.2,
	}
}

// NewSubscriber creates a new block subscriber with reconnection logic
func NewSubscriber(cfg SubscriberConfig) (*Subscriber, error) {
	if len(cfg.WebSocketURLs) == 0 {
		return nil, fmt.Errorf("at least one WebSocket URL is required")
	}

	// Set defaults
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 5 * time.Second
	}
	if cfg.MessageTimeout == 0 {
		cfg.MessageTimeout = 60 * time.Second
	}
	if cfg.ReconnectConfig.MaxBackoff == 0 {
		cfg.ReconnectConfig = DefaultReconnectConfig()
	}

	return &Subscriber{
		wsURLs:            cfg.WebSocketURLs,
		currentURLIdx:     0,
		logger:            cfg.Logger,
		metrics:           cfg.Metrics,
		reconnectConfig:   cfg.ReconnectConfig,
		heartbeatInterval: cfg.HeartbeatInterval,
		heartbeatTimeout:  cfg.HeartbeatTimeout,
		messageTimeout:    cfg.MessageTimeout,
	}, nil
}

// Subscribe subscribes to new block headers and returns channels for blocks and errors
func (s *Subscriber) Subscribe(ctx context.Context) (<-chan *Block, <-chan error, error) {
	blockCh := make(chan *Block, 10)
	errCh := make(chan error, 10)

	// Initial connection
	if err := s.connect(ctx); err != nil {
		return nil, nil, fmt.Errorf("initial connection failed: %w", err)
	}

	// Start subscription loop
	go s.subscriptionLoop(ctx, blockCh, errCh)

	return blockCh, errCh, nil
}

// connect establishes WebSocket connection to Ethereum node
func (s *Subscriber) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try current URL
	url := s.wsURLs[s.currentURLIdx]

	s.logger.Info("connecting to Ethereum WebSocket",
		"url", url,
		"attempt", s.reconnectAttempts+1,
	)

	client, err := ethclient.DialContext(ctx, url)
	if err != nil {
		// Try next URL
		s.currentURLIdx = (s.currentURLIdx + 1) % len(s.wsURLs)
		return fmt.Errorf("failed to connect to %s: %w", url, err)
	}

	// Verify connection by fetching latest block
	_, err = client.BlockNumber(ctx)
	if err != nil {
		client.Close()
		s.currentURLIdx = (s.currentURLIdx + 1) % len(s.wsURLs)
		return fmt.Errorf("connection verification failed for %s: %w", url, err)
	}

	s.client = client
	s.isConnected = true
	s.reconnectAttempts = 0

	s.logger.Info("connected to Ethereum WebSocket", "url", url)

	// Record metric
	if s.metrics != nil {
		s.metrics.RecordWebSocketConnection(ctx, url, true)
	}

	return nil
}

// subscriptionLoop manages the subscription lifecycle with automatic reconnection
func (s *Subscriber) subscriptionLoop(ctx context.Context, blockCh chan<- *Block, errCh chan<- error) {
	defer close(blockCh)
	defer close(errCh)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("context cancelled, stopping subscription")
			s.disconnect()
			return
		default:
			// Subscribe to new heads
			if err := s.subscribeToBlocks(ctx, blockCh, errCh); err != nil {
				s.logger.LogError(ctx, "subscription error", err)

				// Send error to error channel
				select {
				case errCh <- err:
				default:
				}

				// Disconnect and attempt reconnection
				s.disconnect()

				// Calculate backoff delay
				delay := s.calculateReconnectDelay()

				s.logger.Info("reconnecting after delay",
					"delay_seconds", delay.Seconds(),
					"attempts", s.reconnectAttempts,
				)

				// Record reconnection metric
				if s.metrics != nil {
					s.metrics.RecordWebSocketReconnection(ctx, s.reconnectAttempts)
				}

				// Wait before reconnecting
				select {
				case <-time.After(delay):
					// Continue to reconnect
				case <-ctx.Done():
					return
				}

				// Attempt reconnection
				s.reconnectAttempts++
				if err := s.connect(ctx); err != nil {
					s.logger.LogError(ctx, "reconnection failed", err,
						"attempts", s.reconnectAttempts,
					)
					continue
				}

				s.logger.Info("reconnected successfully",
					"attempts", s.reconnectAttempts,
				)
			}
		}
	}
}

// subscribeToBlocks subscribes to new block headers
func (s *Subscriber) subscribeToBlocks(ctx context.Context, blockCh chan<- *Block, errCh chan<- error) error {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("client not connected")
	}

	// Create channel for headers
	headers := make(chan *types.Header, 10)

	// Subscribe to new heads
	sub, err := client.SubscribeNewHead(ctx, headers)
	if err != nil {
		return fmt.Errorf("failed to subscribe to new heads: %w", err)
	}
	defer sub.Unsubscribe()

	s.logger.Info("subscribed to new block headers")

	// Monitor for new blocks with timeout detection
	lastMessageTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-sub.Err():
			if err != nil {
				return fmt.Errorf("subscription error: %w", err)
			}
			return fmt.Errorf("subscription closed")

		case header := <-headers:
			lastMessageTime = time.Now()

			// Convert to our Block type
			block := &Block{
				Number:     header.Number,
				Hash:       header.Hash(),
				Timestamp:  header.Time,
				ParentHash: header.ParentHash,
			}

			// Validate block
			if !block.IsValid() {
				s.logger.Warn("received invalid block",
					"block_number", header.Number,
				)
				continue
			}

			// Check for gaps in block sequence
			s.mu.RLock()
			lastBlockNum := s.lastBlockNumber
			s.mu.RUnlock()

			if lastBlockNum > 0 && block.Number.Uint64() > lastBlockNum+1 {
				gap := block.Number.Uint64() - lastBlockNum - 1
				s.logger.Warn("detected block gap",
					"last_block", lastBlockNum,
					"new_block", block.Number.Uint64(),
					"gap_size", gap,
				)

				// Record gap metric
				if s.metrics != nil {
					s.metrics.RecordBlockGap(ctx, int64(gap))
				}
			}

			// Update last block number
			s.mu.Lock()
			s.lastBlockNumber = block.Number.Uint64()
			s.mu.Unlock()

			// Log block received
			s.logger.Info("new block received",
				"block_number", block.Number.Uint64(),
				"block_hash", block.Hash.Hex(),
				"timestamp", block.Timestamp,
			)

			// Record block metric
			if s.metrics != nil {
				s.metrics.RecordBlockReceived(ctx, block.Number.Uint64())
			}

			// Send block to channel
			select {
			case blockCh <- block:
			case <-ctx.Done():
				return ctx.Err()
			}

		case <-time.After(s.messageTimeout):
			// No message received within timeout
			timeSinceLastMessage := time.Since(lastMessageTime)
			if timeSinceLastMessage > s.messageTimeout {
				s.logger.Warn("message timeout detected",
					"timeout_seconds", s.messageTimeout.Seconds(),
					"last_message_ago", timeSinceLastMessage.Seconds(),
				)
				return fmt.Errorf("no messages received for %v", timeSinceLastMessage)
			}
		}
	}
}

// calculateReconnectDelay calculates delay before next reconnection attempt
func (s *Subscriber) calculateReconnectDelay() time.Duration {
	// Use resilience package for backoff calculation
	retryConfig := resilience.RetryConfig{
		BaseDelay: s.reconnectConfig.BaseDelay,
		MaxDelay:  s.reconnectConfig.MaxDelay,
		Jitter:    s.reconnectConfig.Jitter,
	}

	// Calculate exponential backoff with jitter
	// Note: This is a simplified version - resilience package handles this internally
	delay := s.reconnectConfig.BaseDelay
	for i := 0; i < s.reconnectAttempts && delay < s.reconnectConfig.MaxDelay; i++ {
		delay *= 2
	}

	if delay > s.reconnectConfig.MaxDelay {
		delay = s.reconnectConfig.MaxDelay
	}

	// Add jitter
	if retryConfig.Jitter > 0 {
		jitterAmount := float64(delay) * retryConfig.Jitter
		jitterRange := jitterAmount * 2
		delay = time.Duration(float64(delay) - jitterAmount + (float64(time.Now().UnixNano()%1000000) / 1000000.0 * jitterRange))
	}

	return delay
}

// disconnect closes the current connection
func (s *Subscriber) disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		s.client.Close()
		s.client = nil
	}

	s.isConnected = false

	s.logger.Info("disconnected from Ethereum WebSocket")
}

// IsConnected returns whether the subscriber is currently connected
func (s *Subscriber) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isConnected
}

// GetLastBlockNumber returns the last processed block number
func (s *Subscriber) GetLastBlockNumber() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBlockNumber
}

// Close gracefully shuts down the subscriber
func (s *Subscriber) Close() {
	s.disconnect()
	s.logger.Info("subscriber closed")
}
