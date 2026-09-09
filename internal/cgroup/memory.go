package cgroup

import (
	"errors"
	"math"
)

var ErrMemoryBudget = errors.New("insufficient or invalid extraction memory budget")

// ExtractionMemory estimates peak launcher storage. Cache page pressure is included as
// one output buffer; reclaimability makes this an estimate, not a total-memory bound.
func ExtractionMemory(payload, dictionary, decoder, reserve int64) (int64, error) {
	total := int64(0)
	for _, value := range []int64{payload, dictionary, decoder, reserve} {
		if value < 0 || value > math.MaxInt64-total {
			return 0, ErrMemoryBudget
		}
		total += value
	}
	return total, nil
}

// RetainedMemoryLimit deducts kernel-backed executable storage once from a raw ceiling.
// Zero means no usable budget; it must not be treated as an unlimited ceiling.
func RetainedMemoryLimit(limit, retained int64) int64 {
	if retained < 0 || retained >= limit {
		return 0
	}
	return limit - retained
}
