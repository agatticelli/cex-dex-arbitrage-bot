// Package worker provides a generic worker pool for concurrent task execution.
package worker

import (
	"context"
	"sync"
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

// Pool is a worker pool that processes jobs concurrently.
// It maintains a fixed number of worker goroutines that pull jobs from a queue.
type Pool struct {
	workers  int
	jobQueue chan Job
	results  chan Result
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
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
	if workers <= 0 {
		workers = 1
	}
	if queueSize < 0 {
		queueSize = 0
	}

	poolCtx, cancel := context.WithCancel(ctx)

	p := &Pool{
		workers:  workers,
		jobQueue: make(chan Job, queueSize),
		results:  make(chan Result, queueSize),
		ctx:      poolCtx,
		cancel:   cancel,
	}

	// Start workers
	for i := 0; i < workers; i++ {
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
// It blocks if the queue is full until space is available or context is cancelled.
// Returns an error if the pool is closed or context is cancelled.
func (p *Pool) Submit(job Job) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.jobQueue <- job:
		return nil
	}
}

// SubmitAndWait submits multiple jobs and waits for all results.
// Returns results in the order they complete (not submission order).
func (p *Pool) SubmitAndWait(jobs []Job) []Result {
	// Submit all jobs
	for _, job := range jobs {
		if err := p.Submit(job); err != nil {
			// Context cancelled, return partial results
			break
		}
	}

	// Collect results
	results := make([]Result, 0, len(jobs))
	for i := 0; i < len(jobs); i++ {
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
