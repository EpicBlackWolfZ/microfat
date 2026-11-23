package cgroup

import (
	"errors"
	"math"
)

var ErrMemoryBudget = errors.New("insufficient or invalid extraction memory budget")

/*@
ghost
decreases
pure func extractionPrefix(i int, payload, dictionary, decoder, reserve int64) integer {
	return (i > 0 ? integer(payload) : 0) + (i > 1 ? integer(dictionary) : 0) +
		(i > 2 ? integer(decoder) : 0) + (i > 3 ? integer(reserve) : 0)
}

ghost
decreases
pure func extractionFits(payload, dictionary, decoder, reserve int64) bool {
	return 0 <= payload && 0 <= dictionary && 0 <= decoder && 0 <= reserve &&
		extractionPrefix(4, payload, dictionary, decoder, reserve) <= math.MaxInt64
}
@*/

// ExtractionMemory estimates peak launcher storage. Cache page pressure is included as
// one output buffer; reclaimability makes this an estimate, not a total-memory bound.
// @ requires ErrMemoryBudget != nil && isComparable(ErrMemoryBudget)
// @ ensures (err == nil) == extractionFits(payload, dictionary, decoder, reserve)
// @ ensures err == nil ==> integer(result) == extractionPrefix(4, payload, dictionary, decoder, reserve)
// @ ensures err != nil ==> result == 0 && isComparable(err) && err == ErrMemoryBudget
// @ decreases
func ExtractionMemory(payload, dictionary, decoder, reserve int64) (
	/*@ result @*/ int64,
	/*@ err @*/ error,
) {
	components := [...]int64{payload, dictionary, decoder, reserve}
	total := int64(0)
	// @ invariant 0 <= i && i <= 4
	// @ invariant 0 <= total && integer(total) <= math.MaxInt64
	// @ invariant components[0] == payload && components[1] == dictionary && components[2] == decoder && components[3] == reserve
	// @ invariant integer(total) == extractionPrefix(i, payload, dictionary, decoder, reserve)
	// @ invariant i > 0 ==> 0 <= payload
	// @ invariant i > 1 ==> 0 <= dictionary
	// @ invariant i > 2 ==> 0 <= decoder
	// @ invariant i > 3 ==> 0 <= reserve
	// @ decreases 4 - i
	for _, value := range components /*@ with i @*/ {
		// @ assert value == components[i]
		// @ assert extractionPrefix(i + 1, payload, dictionary, decoder, reserve) == integer(total) + integer(value)
		if value < 0 || value > math.MaxInt64-total {
			// @ assert !extractionFits(payload, dictionary, decoder, reserve)
			return 0, ErrMemoryBudget
		}
		total += value
	}
	return total, nil
}

// RetainedMemoryLimit deducts kernel-backed executable storage once from a raw ceiling.
// Zero means no usable budget; it must not be treated as an unlimited ceiling.
// @ ensures 0 <= retained && retained < limit ==> integer(result) == integer(limit) - integer(retained)
// @ ensures retained < 0 || retained >= limit ==> result == 0
// @ decreases
func RetainedMemoryLimit(limit, retained int64) /*@ (result @*/ int64 /*@ ) @*/ {
	if retained < 0 || retained >= limit {
		return 0
	}
	return limit - retained
}

/*@
ghost
decreases
pure func zstdDecoderFits(window, maxWindow, overhead, maxDecoder int64) bool {
	return 0 < window && window <= maxWindow && 0 <= overhead && 0 < maxDecoder &&
		2*integer(window)+integer(overhead) <= math.MaxInt64 &&
		2*integer(window)+integer(overhead) <= integer(maxDecoder)
}
@*/

// ZstdDecoderMemory estimates Zstd decoder workspace as 2*window + overhead,
// bounding both the sliding window and total decoder memory against overflow.
// @ requires ErrMemoryBudget != nil && isComparable(ErrMemoryBudget)
// @ ensures (err == nil) == zstdDecoderFits(window, maxWindow, overhead, maxDecoder)
// @ ensures err == nil ==> integer(result) == 2*integer(window) + integer(overhead)
// @ ensures err != nil ==> result == 0 && isComparable(err) && err == ErrMemoryBudget
// @ decreases
func ZstdDecoderMemory(window, maxWindow, overhead, maxDecoder int64) (
	/*@ result @*/ int64,
	/*@ err @*/ error,
) {
	if window <= 0 || maxWindow <= 0 || overhead < 0 || maxDecoder <= 0 || window > maxWindow {
		return 0, ErrMemoryBudget
	}
	if window > math.MaxInt64-window {
		return 0, ErrMemoryBudget
	}
	doubled := window + window
	if overhead > math.MaxInt64-doubled {
		return 0, ErrMemoryBudget
	}
	total := doubled + overhead
	if total > maxDecoder {
		return 0, ErrMemoryBudget
	}
	return total, nil
}
