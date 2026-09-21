package cgroup

import (
	"math"
	"strconv"
	"strings"
)

// ResolveBatchGOGC requires a finite memory limit that the caller will apply or
// has observed active. MaxInt64 is Go's unlimited sentinel. Explicit user GOGC
// settings are handled by the caller and are not restricted by this policy.
func (p *TuningPlan) ResolveBatchGOGC(memoryLimit int64) {
	if p.GCProfile != GCProfileBatchETL {
		return
	}
	if memoryLimit <= 0 || memoryLimit == math.MaxInt64 {
		p.GOGC, p.GOGCStr, p.GOGCApplied = 0, "", false
		p.GOGCSkippedReason = "batch_etl GOGC tuning skipped (no finite effective memory ceiling)"
		return
	}
	p.GOGC, p.GOGCStr, p.GOGCApplied = DefaultBatchETLGOGC, "off", true
	p.GOGCSkippedReason = ""
}

// RuntimeMemoryLimit parses Go's GOMEMLIMIT syntax conservatively. An empty,
// disabled, invalid, zero, overflowing or unlimited value is not a finite limit.
func RuntimeMemoryLimit(value string) int64 {
	multiplier := int64(1)
	const unitBase = 1024
	for i, suffix := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if strings.HasSuffix(value, suffix) && (suffix != "B" || !strings.HasSuffix(value, "iB")) {
			value = strings.TrimSuffix(value, suffix)
			for range i {
				multiplier *= unitBase
			}
			break
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/multiplier || n*multiplier == math.MaxInt64 {
		return 0
	}
	return n * multiplier
}
