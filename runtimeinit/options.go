// Package runtimeinit provides automated and programmatic container cgroup resource auto-tuning
// (GOMEMLIMIT, GOMAXPROCS, and workload-aware GOGC) for standalone Go services.
package runtimeinit

import (
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

// Profile represents a workload-aware Go runtime garbage collection tuning profile.
type Profile = cgroup.GCProfile

const (
	// ProfileDefault represents the standard Go runtime default behavior (GOGC=100 or user-provided).
	ProfileDefault Profile = cgroup.GCProfileDefault

	// ProfileLatencyCritical tunes GOGC=75 to minimize GC latency spikes and tail SLAs.
	ProfileLatencyCritical Profile = cgroup.GCProfileLatencyCritical

	// ProfileMemoryConstrained tunes GOGC=40 and default MemoryRatio=0.80 for tight memory containers.
	ProfileMemoryConstrained Profile = cgroup.GCProfileMemoryConstrained

	// ProfileBatchETL tunes GOGC=-1 (off) to rely on GOMEMLIMIT soft ceiling and maximize CPU throughput.
	ProfileBatchETL Profile = cgroup.GCProfileBatchETL

	// ProfileAdaptive dynamically calculates GOGC based on estimated steady-state live heap and headroom.
	ProfileAdaptive Profile = cgroup.GCProfileAdaptive
)

type config struct {
	memoryRatio           float64
	minHeadroomBytes      int64
	cgroupRoot            string
	logger                func(format string, args ...any)
	profile               Profile
	liveHeapEstimateBytes int64
	explicitGOGC          *int
}

// Option configures auto-tuning behavior for AutoTune.
type Option func(*config)

func defaultConfig() *config {
	return &config{
		memoryRatio:           cgroup.DefaultMemoryRatio,
		minHeadroomBytes:      cgroup.DefaultMinHeadroomBytes,
		cgroupRoot:            "",
		logger:                nil,
		profile:               ProfileDefault,
		liveHeapEstimateBytes: 0,
		explicitGOGC:          nil,
	}
}

// WithMemoryRatio configures the fraction of container memory to allocate to GOMEMLIMIT (e.g. 0.85 for 85%).
// If ratio <= 0 or ratio > 1.0, the default ratio (90%) is preserved.
func WithMemoryRatio(ratio float64) Option {
	return func(c *config) {
		if ratio > 0 && ratio <= 1.0 {
			c.memoryRatio = ratio
		}
	}
}

// WithMinHeadroom sets the minimum memory headroom in bytes reserved for non-heap memory allocations.
func WithMinHeadroom(minHeadroomBytes int64) Option {
	return func(c *config) {
		if minHeadroomBytes > 0 {
			c.minHeadroomBytes = minHeadroomBytes
		}
	}
}

// WithCgroupRoot specifies a custom root path to inspect cgroup mounts (useful for testing or non-standard mounts).
func WithCgroupRoot(root string) Option {
	return func(c *config) {
		c.cgroupRoot = root
	}
}

// WithLogger sets a custom logger callback for diagnostic log messages.
func WithLogger(logger func(format string, args ...any)) Option {
	return func(c *config) {
		c.logger = logger
	}
}

// WithProfile configures the workload-aware GC tuning profile (e.g. ProfileLatencyCritical, ProfileBatchETL).
func WithProfile(p Profile) Option {
	return func(c *config) {
		if p != "" {
			c.profile = p
		}
	}
}

// WithLiveHeapEstimate sets the estimated peak steady-state live heap size in bytes for ProfileAdaptive.
func WithLiveHeapEstimate(bytes int64) Option {
	return func(c *config) {
		if bytes > 0 {
			c.liveHeapEstimateBytes = bytes
		}
	}
}

// WithLiveHeapEstimateString parses and sets the live heap size (e.g. "150MB", "150MiB", "150M") for ProfileAdaptive.
func WithLiveHeapEstimateString(s string) Option {
	return func(c *config) {
		if val, err := cgroup.ParseByteSize(s); err == nil && val > 0 {
			c.liveHeapEstimateBytes = val
		}
	}
}

// WithGOGC configures an explicit programmatic target GOGC value (-1 for off).
func WithGOGC(gogc int) Option {
	return func(c *config) {
		c.explicitGOGC = &gogc
	}
}
