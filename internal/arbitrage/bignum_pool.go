package arbitrage

import (
	"math/big"
	"sync"
)

var bigFloatPool = sync.Pool{
	New: func() interface{} {
		return new(big.Float)
	},
}

var bigIntPool = sync.Pool{
	New: func() interface{} {
		return new(big.Int)
	},
}

func AcquireBigFloat() *big.Float {
	bf := bigFloatPool.Get().(*big.Float)
	bf.SetFloat64(0)
	return bf
}

func ReleaseBigFloat(bf *big.Float) {
	if bf == nil {
		return
	}
	bigFloatPool.Put(bf)
}

func AcquireBigInt() *big.Int {
	bi := bigIntPool.Get().(*big.Int)
	bi.SetInt64(0)
	return bi
}

func ReleaseBigInt(bi *big.Int) {
	if bi == nil {
		return
	}
	bigIntPool.Put(bi)
}

type BigFloatBatch struct {
	values []*big.Float
}

func NewBigFloatBatch() *BigFloatBatch {
	return &BigFloatBatch{
		values: make([]*big.Float, 0, 8),
	}
}

func (b *BigFloatBatch) Acquire() *big.Float {
	bf := AcquireBigFloat()
	b.values = append(b.values, bf)
	return bf
}

func (b *BigFloatBatch) Release() {
	for _, bf := range b.values {
		ReleaseBigFloat(bf)
	}
	b.values = b.values[:0]
}

type BigIntBatch struct {
	values []*big.Int
}

func NewBigIntBatch() *BigIntBatch {
	return &BigIntBatch{
		values: make([]*big.Int, 0, 8),
	}
}

func (b *BigIntBatch) Acquire() *big.Int {
	bi := AcquireBigInt()
	b.values = append(b.values, bi)
	return bi
}

func (b *BigIntBatch) Release() {
	for _, bi := range b.values {
		ReleaseBigInt(bi)
	}
	b.values = b.values[:0]
}
