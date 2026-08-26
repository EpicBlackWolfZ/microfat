// Package runtimeinit provides automated and programmatic container cgroup resource auto-tuning
// (GOMEMLIMIT and GOMAXPROCS) for standalone Go services.
package runtimeinit

import (
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

type config struct {
	memoryRatio      float64
	minHeadroomBytes int64
	cgroupRoot       string
	logger           func(format string, args ...any)
}

// Option configures auto-tuning behavior for AutoTune.
type Option func(*config)

func defaultConfig() *config {
	return &config{
		memoryRatio:      cgroup.DefaultMemoryRatio,
		minHeadroomBytes: cgroup.DefaultMinHeadroomBytes,
		cgroupRoot:       "",
		logger:           nil,
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
