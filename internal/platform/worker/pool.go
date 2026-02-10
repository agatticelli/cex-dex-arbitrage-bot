// Package worker provides a generic worker pool for concurrent task execution.
package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrBackpressure is returned when the job queue is full and drop policy is DropNewest
var ErrBackpressure = errors.New("job queue full: backpressure applied")

// DropPolicy defines how to handle new jobs when the queue is full
type DropPolicy int

const (
	// DropPolicyBlock blocks until space is available (default behavior)
	DropPolicyBlock DropPolicy = iota
	// DropPolicyNewest rejects new jobs when queue is full
	DropPolicyNewest
	// DropPolicyOldest drops the oldest job to make room for new ones
	DropPolicyOldest
)

// Job represents a unit of work to be executed by a worker.
type Job struct {
	// ID is an optional identifier for the job (useful for logging/debugging)
	ID string
	// Execute is the function to run. It receives a context and returns a result and error.
	Execute func(ctx context.Context) (interface{}, error)
}

// Result represents the outcome of a job execution.
type Result struct {
	// JobID is the ID of the job that produced this result
	JobID string
	// Value is the result of the job execution (nil if error)
	Value interface{}
	// Err is the error from job execution (nil if successful)
	Err error
}

// PoolStats holds statistics about the pool
type PoolStats struct {
	JobsSubmitted uint64
	JobsCompleted uint64
	JobsDropped   uint64
	QueueLength   int
}

// Pool is a worker pool that processes jobs concurrently.
// It maintains a fixed number of worker goroutines that pull jobs from a queue.
type Pool struct {
	workers    int
	jobQueue   chan Job
	results    chan Result
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	dropPolicy DropPolicy

	// Statistics (atomic for thread-safety)
	jobsSubmitted uint64
	jobsCompleted uint64
	jobsDropped   uint64
}

// PoolConfig holds configuration for creating a new pool
type PoolConfig struct {
	Workers    int
	QueueSize  int
	DropPolicy DropPolicy
}

// NewPool creates a new worker pool with the specified number of workers.
// The pool starts immediately and workers begin waiting for jobs.
//
// Parameters:
//   - ctx: Parent context for cancellation
//   - workers: Number of concurrent workers (goroutines)
//   - queueSize: Size of the job queue buffer (0 for unbuffered)
//
// Example:
//
//	pool := worker.NewPool(ctx, 4, 100)
//	defer pool.Close()
//	pool.Submit(worker.Job{ID: "job1", Execute: func(ctx) (interface{}, error) { ... }})
func NewPool(ctx context.Context, workers int, queueSize int) *Pool {
	return NewPoolWithConfig(ctx, PoolConfig{
		Workers:    workers,
		QueueSize:  queueSize,
		DropPolicy: DropPolicyBlock,
	})
}

// NewPoolWithConfig creates a new worker pool with custom configuration.
func NewPoolWithConfig(ctx context.Context, cfg PoolConfig) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.QueueSize < 0 {
		cfg.QueueSize = 0
	}

	poolCtx, cancel := context.WithCancel(ctx)

	p := &Pool{
		workers:    cfg.Workers,
		jobQueue:   make(chan Job, cfg.QueueSize),
		results:    make(chan Result, cfg.QueueSize),
		ctx:        poolCtx,
		cancel:     cancel,
		dropPolicy: cfg.DropPolicy,
	}

	// Start workers
	for i := 0; i < cfg.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	return p
}

// worker is the main worker goroutine loop.
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case job, ok := <-p.jobQueue:
			if !ok {
				return // Queue closed
			}
			// Execute the job
			value, err := job.Execute(p.ctx)
			atomic.AddUint64(&p.jobsCompleted, 1)

			// Send result (non-blocking if channel full, result is dropped)
			select {
			case p.results <- Result{JobID: job.ID, Value: value, Err: err}:
			default:
				// Results channel full, drop result
			}
		}
	}
}

// Submit adds a job to the pool's queue.
// Behavior depends on the configured DropPolicy:
//   - DropPolicyBlock: blocks until space is available (default)
//   - DropPolicyNewest: returns ErrBackpressure if queue is full
//   - DropPolicyOldest: drops oldest job to make room for new one
//
// Returns an error if the pool is closed, context is cancelled, or backpressure applied.
func (p *Pool) Submit(job Job) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	default:
	}

	switch p.dropPolicy {
	case DropPolicyBlock:
		// Default: block until space available
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		case p.jobQueue <- job:
			atomic.AddUint64(&p.jobsSubmitted, 1)
			return nil
		}

	case DropPolicyNewest:
		// Non-blocking: reject if full
		select {
		case p.jobQueue <- job:
			atomic.AddUint64(&p.jobsSubmitted, 1)
			return nil
		default:
			atomic.AddUint64(&p.jobsDropped, 1)
			return ErrBackpressure
		}

	case DropPolicyOldest:
		// Non-blocking: drop oldest if full
		select {
		case p.jobQueue <- job:
			atomic.AddUint64(&p.jobsSubmitted, 1)
			return nil
		default:
			// Queue full, try to drop oldest
			select {
			case <-p.jobQueue:
				atomic.AddUint64(&p.jobsDropped, 1)
			default:
				// Queue became empty, try again
			}
			// Try to submit again
			select {
			case p.jobQueue <- job:
				atomic.AddUint64(&p.jobsSubmitted, 1)
				return nil
			default:
				atomic.AddUint64(&p.jobsDropped, 1)
				return ErrBackpressure
			}
		}
	}

	return nil
}

// TrySubmit attempts to submit a job without blocking.
// Returns ErrBackpressure if the queue is full.
func (p *Pool) TrySubmit(job Job) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.jobQueue <- job:
		atomic.AddUint64(&p.jobsSubmitted, 1)
		return nil
	default:
		return ErrBackpressure
	}
}

// SubmitAndWait submits multiple jobs and waits for all results.
// Returns results in the order they complete (not submission order).
// If submission fails partway through, only waits for successfully submitted jobs.
func (p *Pool) SubmitAndWait(jobs []Job) []Result {
	// Submit all jobs, tracking how many were successfully submitted
	submitted := 0
	for _, job := range jobs {
		if err := p.Submit(job); err != nil {
			// Context cancelled or queue full, stop submitting
			break
		}
		submitted++
	}

	// Only collect results for jobs that were actually submitted
	// This prevents deadlock when Submit fails partway through
	results := make([]Result, 0, submitted)
	for i := 0; i < submitted; i++ {
		select {
		case <-p.ctx.Done():
			return results
		case result := <-p.results:
			results = append(results, result)
		}
	}

	return results
}

// Results returns the results channel for consuming job results.
// Callers should read from this channel to receive job outcomes.
func (p *Pool) Results() <-chan Result {
	return p.results
}

// Close gracefully shuts down the pool.
// It stops accepting new jobs and waits for all workers to finish.
func (p *Pool) Close() {
	p.cancel()
	close(p.jobQueue)
	p.wg.Wait()
	close(p.results)
}

// Workers returns the number of workers in the pool.
func (p *Pool) Workers() int {
	return p.workers
}

// QueueLen returns the current number of jobs waiting in the queue.
func (p *Pool) QueueLen() int {
	return len(p.jobQueue)
}

// Stats returns current pool statistics.
func (p *Pool) Stats() PoolStats {
	return PoolStats{
		JobsSubmitted: atomic.LoadUint64(&p.jobsSubmitted),
		JobsCompleted: atomic.LoadUint64(&p.jobsCompleted),
		JobsDropped:   atomic.LoadUint64(&p.jobsDropped),
		QueueLength:   len(p.jobQueue),
	}
}

// DropPolicy returns the configured drop policy.
func (p *Pool) DropPolicy() DropPolicy {
	return p.dropPolicy
}
