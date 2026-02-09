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
	clientPool         *ClientPool   // NEW: for HTTP RPC fallback
	pollInterval       time.Duration // NEW: how often to poll when WS down (default 12s = 1 block)
	maxWSFailures      int           // NEW: switch to HTTP after N consecutive WS failures (default 3)
	wsFailureCount     int           // NEW: track consecutive failures
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
	ClientPool        *ClientPool   // NEW: for HTTP RPC fallback
	PollInterval      time.Duration // NEW: polling interval when WS down (default 12s)
	MaxWSFailures     int           // NEW: max WS failures before HTTP fallback (default 3)
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
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 12 * time.Second // Default: ~1 block time
	}
	if cfg.MaxWSFailures == 0 {
		cfg.MaxWSFailures = 3 // Default: switch after 3 failures
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
		clientPool:        cfg.ClientPool,
		pollInterval:      cfg.PollInterval,
		maxWSFailures:     cfg.MaxWSFailures,
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

// subscriptionLoop manages the subscription lifecycle with automatic reconnection and HTTP fallback
func (s *Subscriber) subscriptionLoop(ctx context.Context, blockCh chan<- *Block, errCh chan<- error) {
	defer close(blockCh)
	defer close(errCh)

	wsMode := true // Start in WebSocket mode

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("context cancelled, stopping subscription")
			s.disconnect()
			return
		default:
			if wsMode {
				// Try WebSocket subscription
				if err := s.subscribeToBlocks(ctx, blockCh, errCh); err != nil {
					s.logger.LogError(ctx, "subscription error", err)

					// Send error to error channel
					select {
					case errCh <- err:
					default:
					}

					// Increment failure count
					s.wsFailureCount++

					// Check if we should switch to HTTP polling
					if s.clientPool != nil && s.wsFailureCount >= s.maxWSFailures {
						s.logger.Warn("switching to HTTP polling fallback",
							"ws_failures", s.wsFailureCount,
						)
						wsMode = false
						continue
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

					// Reset failure count on successful connection
					s.wsFailureCount = 0
				}
			} else {
				// HTTP polling mode
				s.logger.Info("running in HTTP polling mode",
					"poll_interval", s.pollInterval.Seconds(),
				)

				if err := s.pollBlocks(ctx, blockCh, errCh); err != nil {
					s.logger.LogError(ctx, "HTTP polling error", err)

					// Send error to error channel
					select {
					case errCh <- err:
					default:
					}

					// Wait before retrying
					select {
					case <-time.After(s.pollInterval):
					case <-ctx.Done():
						return
					}
				}

				// Periodically try to switch back to WebSocket
				if s.wsFailureCount > 0 {
					s.wsFailureCount-- // Decay failure count
				}

				if s.wsFailureCount == 0 {
					s.logger.Info("attempting to switch back to WebSocket mode")
					if err := s.connect(ctx); err != nil {
						s.logger.Warn("failed to reconnect to WebSocket, staying in HTTP mode", "error", err)
						s.wsFailureCount = 1 // Prevent immediate retry
					} else {
						wsMode = true
						s.logger.Info("successfully switched back to WebSocket mode")
					}
				}
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
				s.logger.Warn("detected block gap - initiating recovery",
					"last_block", lastBlockNum,
					"new_block", block.Number.Uint64(),
					"gap_size", gap,
				)

				// Record gap metric
				if s.metrics != nil {
					s.metrics.RecordBlockGap(ctx, int64(gap))
				}

				// Attempt to backfill missing blocks via RPC
				if s.clientPool != nil {
					err := s.backfillBlocks(ctx, lastBlockNum+1, block.Number.Uint64()-1, blockCh)
					if err != nil {
						s.logger.LogError(ctx, "gap recovery failed", err,
							"first_missing", lastBlockNum+1,
							"last_missing", block.Number.Uint64()-1,
						)
						// Continue anyway with current block
					}
				} else {
					s.logger.Warn("client pool not configured, cannot backfill gap",
						"gap_size", gap,
					)
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

// backfillBlocks fetches missing blocks via RPC to recover from gaps
func (s *Subscriber) backfillBlocks(ctx context.Context, startBlock, endBlock uint64, blockCh chan<- *Block) error {
	if s.clientPool == nil {
		return fmt.Errorf("client pool not configured for backfilling")
	}

	s.logger.Info("backfilling missing blocks",
		"start", startBlock,
		"end", endBlock,
		"count", endBlock-startBlock+1,
	)

	// Get client from pool
	client, err := s.clientPool.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get HTTP client for backfill: %w", err)
	}

	// Fetch each missing block
	for blockNum := startBlock; blockNum <= endBlock; blockNum++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch block header via RPC
		header, err := client.HeaderByNumber(ctx, big.NewInt(int64(blockNum)))
		if err != nil {
			s.logger.LogError(ctx, "failed to fetch block during backfill", err,
				"block", blockNum,
			)
			return fmt.Errorf("failed to fetch block %d: %w", blockNum, err)
		}

		// Convert to our Block type
		block := &Block{
			Number:     header.Number,
			Hash:       header.Hash(),
			Timestamp:  header.Time,
			ParentHash: header.ParentHash,
		}

		// Validate block
		if !block.IsValid() {
			s.logger.Warn("invalid block during backfill",
				"block_number", blockNum,
			)
			continue
		}

		s.logger.Info("backfilled block", "block", blockNum)

		// Send backfilled block to processing pipeline
		select {
		case blockCh <- block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.logger.Info("backfill complete",
		"blocks_recovered", endBlock-startBlock+1,
	)

	return nil
}

// pollBlocks polls for new blocks via HTTP RPC when WebSocket is unavailable
func (s *Subscriber) pollBlocks(ctx context.Context, blockCh chan<- *Block, errCh chan<- error) error {
	if s.clientPool == nil {
		return fmt.Errorf("client pool not configured for HTTP polling")
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	s.logger.Info("starting HTTP polling mode", "poll_interval", s.pollInterval)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			// Get latest block number via HTTP RPC
			blockNum, err := s.clientPool.BlockNumber(ctx)
			if err != nil {
				s.logger.LogError(ctx, "HTTP block number fetch failed", err)
				return fmt.Errorf("HTTP block number fetch failed: %w", err)
			}

			// Check if this is a new block
			s.mu.RLock()
			lastBlock := s.lastBlockNumber
			s.mu.RUnlock()

			if blockNum > lastBlock {
				// Fetch block header via HTTP RPC
				client, err := s.clientPool.GetClient()
				if err != nil {
					s.logger.LogError(ctx, "failed to get HTTP client", err)
					return fmt.Errorf("failed to get HTTP client: %w", err)
				}

				header, err := client.HeaderByNumber(ctx, big.NewInt(int64(blockNum)))
				if err != nil {
					s.logger.LogError(ctx, "HTTP block fetch failed", err)
					return fmt.Errorf("HTTP block fetch failed: %w", err)
				}

				// Convert to our Block type
				block := &Block{
					Number:     header.Number,
					Hash:       header.Hash(),
					Timestamp:  header.Time,
					ParentHash: header.ParentHash,
				}

				// Validate block
				if !block.IsValid() {
					s.logger.Warn("received invalid block via HTTP",
						"block_number", blockNum,
					)
					continue
				}

				// Check for gaps
				if lastBlock > 0 && blockNum > lastBlock+1 {
					gap := blockNum - lastBlock - 1
					s.logger.Warn("detected block gap in HTTP polling",
						"last_block", lastBlock,
						"new_block", blockNum,
						"gap_size", gap,
					)

					// Record gap metric
					if s.metrics != nil {
						s.metrics.RecordBlockGap(ctx, int64(gap))
					}
				}

				// Update last block number
				s.mu.Lock()
				s.lastBlockNumber = blockNum
				s.mu.Unlock()

				// Log block received
				s.logger.Info("new block received via HTTP polling",
					"block_number", blockNum,
					"block_hash", block.Hash.Hex(),
					"timestamp", block.Timestamp,
				)

				// Record block metric
				if s.metrics != nil {
					s.metrics.RecordBlockReceived(ctx, blockNum)
				}

				// Send block to channel
				select {
				case blockCh <- block:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// Close gracefully shuts down the subscriber
func (s *Subscriber) Close() {
	s.disconnect()
	s.logger.Info("subscriber closed")
}
