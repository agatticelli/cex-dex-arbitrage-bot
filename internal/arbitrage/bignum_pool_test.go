package arbitrage

import (
	"math/big"
	"sync"
	"testing"
)

func TestAcquireReleaseBigFloat(t *testing.T) {
	// Acquire should return a zero-valued big.Float
	bf := AcquireBigFloat()
	if bf == nil {
		t.Fatal("AcquireBigFloat returned nil")
	}

	// Should be zero
	if bf.Cmp(big.NewFloat(0)) != 0 {
		t.Errorf("Expected zero value, got %v", bf)
	}

	// Set a value
	bf.SetFloat64(123.456)

	// Release should not panic
	ReleaseBigFloat(bf)

	// Acquire again - should get a reset value
	bf2 := AcquireBigFloat()
	if bf2.Cmp(big.NewFloat(0)) != 0 {
		t.Errorf("Expected zero value after re-acquire, got %v", bf2)
	}
	ReleaseBigFloat(bf2)

	t.Log("✓ AcquireBigFloat/ReleaseBigFloat works correctly")
}

func TestAcquireReleaseBigInt(t *testing.T) {
	// Acquire should return a zero-valued big.Int
	bi := AcquireBigInt()
	if bi == nil {
		t.Fatal("AcquireBigInt returned nil")
	}

	// Should be zero
	if bi.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected zero value, got %v", bi)
	}

	// Set a value
	bi.SetInt64(123456789)

	// Release should not panic
	ReleaseBigInt(bi)

	// Acquire again - should get a reset value
	bi2 := AcquireBigInt()
	if bi2.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected zero value after re-acquire, got %v", bi2)
	}
	ReleaseBigInt(bi2)

	t.Log("✓ AcquireBigInt/ReleaseBigInt works correctly")
}

func TestReleaseNilValues(t *testing.T) {
	// Should not panic on nil
	ReleaseBigFloat(nil)
	ReleaseBigInt(nil)

	t.Log("✓ Release handles nil values safely")
}

func TestBigFloatBatch(t *testing.T) {
	batch := NewBigFloatBatch()

	// Acquire multiple values
	v1 := batch.Acquire()
	v2 := batch.Acquire()
	v3 := batch.Acquire()

	// Set different values
	v1.SetFloat64(1.0)
	v2.SetFloat64(2.0)
	v3.SetFloat64(3.0)

	// Release all
	batch.Release()

	// Acquire more - batch should be reusable
	v4 := batch.Acquire()
	if v4.Cmp(big.NewFloat(0)) != 0 {
		t.Errorf("Expected zero value after batch release, got %v", v4)
	}
	batch.Release()

	t.Log("✓ BigFloatBatch works correctly")
}

func TestBigIntBatch(t *testing.T) {
	batch := NewBigIntBatch()

	// Acquire multiple values
	v1 := batch.Acquire()
	v2 := batch.Acquire()
	v3 := batch.Acquire()

	// Set different values
	v1.SetInt64(100)
	v2.SetInt64(200)
	v3.SetInt64(300)

	// Release all
	batch.Release()

	// Acquire more - batch should be reusable
	v4 := batch.Acquire()
	if v4.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected zero value after batch release, got %v", v4)
	}
	batch.Release()

	t.Log("✓ BigIntBatch works correctly")
}

func TestConcurrentPoolAccess(t *testing.T) {
	const goroutines = 100
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // For both Float and Int

	// Concurrent big.Float access
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				bf := AcquireBigFloat()
				bf.SetFloat64(float64(j))
				// Simulate some work
				_ = bf.Text('f', 2)
				ReleaseBigFloat(bf)
			}
		}()
	}

	// Concurrent big.Int access
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				bi := AcquireBigInt()
				bi.SetInt64(int64(j))
				// Simulate some work
				_ = bi.String()
				ReleaseBigInt(bi)
			}
		}()
	}

	wg.Wait()
	t.Log("✓ Concurrent pool access works correctly")
}

func TestBatchConcurrentAccess(t *testing.T) {
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			floatBatch := NewBigFloatBatch()
			intBatch := NewBigIntBatch()

			for j := 0; j < 10; j++ {
				bf := floatBatch.Acquire()
				bi := intBatch.Acquire()

				bf.SetFloat64(float64(id * j))
				bi.SetInt64(int64(id * j))
			}

			floatBatch.Release()
			intBatch.Release()
		}(i)
	}

	wg.Wait()
	t.Log("✓ Batch concurrent access works correctly")
}

// BenchmarkBigFloatPool benchmarks pooled vs non-pooled allocation
func BenchmarkBigFloatPool(b *testing.B) {
	b.Run("Pooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bf := AcquireBigFloat()
			bf.SetFloat64(123.456)
			bf.Mul(bf, big.NewFloat(2.0))
			ReleaseBigFloat(bf)
		}
	})

	b.Run("Unpooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bf := new(big.Float)
			bf.SetFloat64(123.456)
			bf.Mul(bf, big.NewFloat(2.0))
		}
	})
}

// BenchmarkBigIntPool benchmarks pooled vs non-pooled allocation
func BenchmarkBigIntPool(b *testing.B) {
	b.Run("Pooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bi := AcquireBigInt()
			bi.SetInt64(123456789)
			bi.Mul(bi, big.NewInt(2))
			ReleaseBigInt(bi)
		}
	})

	b.Run("Unpooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bi := new(big.Int)
			bi.SetInt64(123456789)
			bi.Mul(bi, big.NewInt(2))
		}
	})
}

// BenchmarkBatchVsIndividual compares batch vs individual acquire/release
func BenchmarkBatchVsIndividual(b *testing.B) {
	const count = 10

	b.Run("Batch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			batch := NewBigFloatBatch()
			for j := 0; j < count; j++ {
				bf := batch.Acquire()
				bf.SetFloat64(float64(j))
			}
			batch.Release()
		}
	})

	b.Run("Individual", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			values := make([]*big.Float, count)
			for j := 0; j < count; j++ {
				bf := AcquireBigFloat()
				bf.SetFloat64(float64(j))
				values[j] = bf
			}
			for _, bf := range values {
				ReleaseBigFloat(bf)
			}
		}
	})
}
