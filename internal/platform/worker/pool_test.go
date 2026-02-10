package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPool_Defaults(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 10)
	defer pool.Close()

	if pool.Workers() != 4 {
		t.Errorf("Expected 4 workers, got %d", pool.Workers())
	}

	if pool.DropPolicy() != DropPolicyBlock {
		t.Errorf("Expected DropPolicyBlock, got %d", pool.DropPolicy())
	}
}

func TestNewPoolWithConfig_ZeroWorkers(t *testing.T) {
	ctx := context.Background()
	pool := NewPoolWithConfig(ctx, PoolConfig{
		Workers:   0, // Should default to 1
		QueueSize: 10,
	})
	defer pool.Close()

	if pool.Workers() != 1 {
		t.Errorf("Expected 1 worker (default), got %d", pool.Workers())
	}
}

func TestNewPoolWithConfig_NegativeQueueSize(t *testing.T) {
	ctx := context.Background()
	pool := NewPoolWithConfig(ctx, PoolConfig{
		Workers:   2,
		QueueSize: -5, // Should default to 0
	})
	defer pool.Close()

	// Pool should still work with unbuffered queue
	if pool.Workers() != 2 {
		t.Errorf("Expected 2 workers, got %d", pool.Workers())
	}
}

func TestPool_Submit_Success(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 2, 10)
	defer pool.Close()

	resultCh := make(chan int, 1)

	job := Job{
		ID: "test-job",
		Execute: func(ctx context.Context) (interface{}, error) {
			resultCh <- 42
			return 42, nil
		},
	}

	err := pool.Submit(job)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	select {
	case result := <-resultCh:
		if result != 42 {
			t.Errorf("Expected 42, got %d", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for job execution")
	}
}

func TestPool_Submit_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := NewPool(ctx, 2, 10)
	defer pool.Close()

	cancel() // Cancel immediately

	job := Job{
		ID: "test-job",
		Execute: func(ctx context.Context) (interface{}, error) {
			return nil, nil
		},
	}

	err := pool.Submit(job)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestPool_TrySubmit_QueueFull(t *testing.T) {
	ctx := context.Background()
	// Create pool with very small queue
	pool := NewPoolWithConfig(ctx, PoolConfig{
		Workers:   1,
		QueueSize: 1,
	})
	defer pool.Close()

	// Block the worker
	blocker := make(chan struct{})
	blockingJob := Job{
		ID: "blocking",
		Execute: func(ctx context.Context) (interface{}, error) {
			<-blocker // Wait forever
			return nil, nil
		},
	}
	_ = pool.Submit(blockingJob)

	// Fill the queue
	_ = pool.TrySubmit(Job{ID: "fill", Execute: func(ctx context.Context) (interface{}, error) { return nil, nil }})

	// This should fail with backpressure
	err := pool.TrySubmit(Job{ID: "overflow", Execute: func(ctx context.Context) (interface{}, error) { return nil, nil }})
	if !errors.Is(err, ErrBackpressure) {
		t.Errorf("Expected ErrBackpressure, got %v", err)
	}

	close(blocker) // Unblock
}

func TestPool_DropPolicyNewest(t *testing.T) {
	ctx := context.Background()
	pool := NewPoolWithConfig(ctx, PoolConfig{
		Workers:    1,
		QueueSize:  1,
		DropPolicy: DropPolicyNewest,
	})
	defer pool.Close()

	// Block the worker
	blocker := make(chan struct{})
	blockingJob := Job{
		ID: "blocking",
		Execute: func(ctx context.Context) (interface{}, error) {
			<-blocker
			return nil, nil
		},
	}
	_ = pool.Submit(blockingJob)

	// Fill the queue
	_ = pool.Submit(Job{ID: "fill", Execute: func(ctx context.Context) (interface{}, error) { return nil, nil }})

	// This should return ErrBackpressure with DropPolicyNewest
	err := pool.Submit(Job{ID: "newest", Execute: func(ctx context.Context) (interface{}, error) { return nil, nil }})
	if !errors.Is(err, ErrBackpressure) {
		t.Errorf("Expected ErrBackpressure, got %v", err)
	}

	stats := pool.Stats()
	if stats.JobsDropped < 1 {
		t.Errorf("Expected at least 1 dropped job, got %d", stats.JobsDropped)
	}

	close(blocker)
}

func TestPool_Stats(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 2, 10)
	defer pool.Close()

	// Submit some jobs
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		_ = pool.Submit(Job{
			ID: "job",
			Execute: func(ctx context.Context) (interface{}, error) {
				wg.Done()
				return nil, nil
			},
		})
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond) // Let stats update

	stats := pool.Stats()
	if stats.JobsSubmitted != 5 {
		t.Errorf("Expected 5 submitted jobs, got %d", stats.JobsSubmitted)
	}
	if stats.JobsCompleted != 5 {
		t.Errorf("Expected 5 completed jobs, got %d", stats.JobsCompleted)
	}
}

func TestPool_Results(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 2, 10)
	defer pool.Close()

	expectedResult := "hello"
	_ = pool.Submit(Job{
		ID: "greeting",
		Execute: func(ctx context.Context) (interface{}, error) {
			return expectedResult, nil
		},
	})

	select {
	case result := <-pool.Results():
		if result.JobID != "greeting" {
			t.Errorf("Expected job ID 'greeting', got '%s'", result.JobID)
		}
		if result.Value != expectedResult {
			t.Errorf("Expected '%s', got '%v'", expectedResult, result.Value)
		}
		if result.Err != nil {
			t.Errorf("Expected no error, got %v", result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestPool_Results_WithError(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 2, 10)
	defer pool.Close()

	expectedErr := errors.New("job failed")
	_ = pool.Submit(Job{
		ID: "failing",
		Execute: func(ctx context.Context) (interface{}, error) {
			return nil, expectedErr
		},
	})

	select {
	case result := <-pool.Results():
		if result.Err == nil {
			t.Error("Expected error, got nil")
		}
		if result.Err.Error() != expectedErr.Error() {
			t.Errorf("Expected '%v', got '%v'", expectedErr, result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

func TestPool_SubmitAndWait(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 10)
	defer pool.Close()

	jobs := []Job{
		{ID: "1", Execute: func(ctx context.Context) (interface{}, error) { return 1, nil }},
		{ID: "2", Execute: func(ctx context.Context) (interface{}, error) { return 2, nil }},
		{ID: "3", Execute: func(ctx context.Context) (interface{}, error) { return 3, nil }},
	}

	results := pool.SubmitAndWait(jobs)

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Sum all results (order may vary)
	sum := 0
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("Unexpected error: %v", r.Err)
		}
		if val, ok := r.Value.(int); ok {
			sum += val
		}
	}
	if sum != 6 {
		t.Errorf("Expected sum of 6, got %d", sum)
	}
}

func TestPool_ConcurrentSubmit(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 100)
	defer pool.Close()

	var counter int64
	var wg sync.WaitGroup

	// Submit 100 jobs concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pool.Submit(Job{
				ID: "concurrent",
				Execute: func(ctx context.Context) (interface{}, error) {
					atomic.AddInt64(&counter, 1)
					return nil, nil
				},
			})
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Let jobs complete

	if atomic.LoadInt64(&counter) != 100 {
		t.Errorf("Expected 100 executions, got %d", counter)
	}
}

func TestPool_Close(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 10)

	// Submit a job
	executed := make(chan struct{})
	_ = pool.Submit(Job{
		ID: "before-close",
		Execute: func(ctx context.Context) (interface{}, error) {
			close(executed)
			return nil, nil
		},
	})

	<-executed
	pool.Close()

	// After close, submit should fail
	err := pool.Submit(Job{
		ID: "after-close",
		Execute: func(ctx context.Context) (interface{}, error) {
			return nil, nil
		},
	})

	if err == nil {
		t.Error("Expected error after Close(), got nil")
	}
}

func TestPool_QueueLen(t *testing.T) {
	ctx := context.Background()
	pool := NewPoolWithConfig(ctx, PoolConfig{
		Workers:   1,
		QueueSize: 10,
	})
	defer pool.Close()

	// Block the worker
	blocker := make(chan struct{})
	started := make(chan struct{})
	_ = pool.Submit(Job{
		ID: "blocker",
		Execute: func(ctx context.Context) (interface{}, error) {
			close(started) // Signal that we've started
			<-blocker
			return nil, nil
		},
	})

	// Wait for the blocker to be picked up
	<-started

	// Add jobs to queue
	for i := 0; i < 5; i++ {
		_ = pool.TrySubmit(Job{
			ID: "queued",
			Execute: func(ctx context.Context) (interface{}, error) {
				return nil, nil
			},
		})
	}

	qLen := pool.QueueLen()
	if qLen != 5 {
		t.Errorf("Expected queue length 5, got %d", qLen)
	}

	close(blocker)
}

// Benchmark tests
func BenchmarkPool_Submit(b *testing.B) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 1000)
	defer pool.Close()

	job := Job{
		ID: "bench",
		Execute: func(ctx context.Context) (interface{}, error) {
			return nil, nil
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Submit(job)
	}
}

func BenchmarkPool_SubmitAndWait(b *testing.B) {
	ctx := context.Background()
	pool := NewPool(ctx, 4, 100)
	defer pool.Close()

	jobs := make([]Job, 10)
	for i := 0; i < 10; i++ {
		jobs[i] = Job{
			ID: "bench",
			Execute: func(ctx context.Context) (interface{}, error) {
				return nil, nil
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.SubmitAndWait(jobs)
	}
}
