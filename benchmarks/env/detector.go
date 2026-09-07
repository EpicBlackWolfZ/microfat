// Package env gathers comprehensive host hardware metrics, process execution context,
// and tri-state resource limits for microfat benchmark experiments.
package env

import (
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

// Detector gathers host hardware metrics and process execution context.
type Detector struct {
	cgroupRoot     string
	procCgroupPath string
	procPath       string
	sysPath        string
}

const (
	defaultCgroupRoot     = "/sys/fs/cgroup"
	defaultProcCgroupPath = "/proc/self/cgroup"
	defaultProcPath       = "/proc"
	defaultSysPath        = "/sys"
)

// NewDetector creates a Detector configured with default host filesystem paths.
func NewDetector() *Detector {
	return &Detector{
		cgroupRoot:     defaultCgroupRoot,
		procCgroupPath: defaultProcCgroupPath,
		procPath:       defaultProcPath,
		sysPath:        defaultSysPath,
	}
}

// NewCustomDetector creates a Detector with custom filesystem roots for testing.
func NewCustomDetector(cgroupRoot, procCgroupPath, procPath, sysPath string) *Detector {
	return &Detector{
		cgroupRoot:     cgroupRoot,
		procCgroupPath: procCgroupPath,
		procPath:       procPath,
		sysPath:        sysPath,
	}
}

// Detect gathers and returns the complete EnvironmentSnapshot using the default detector.
func Detect() (*schema.EnvironmentSnapshot, error) {
	return NewDetector().Detect()
}

// Detect gathers host telemetry, process context, and tri-state cgroup resource limits.
func (d *Detector) Detect() (*schema.EnvironmentSnapshot, error) {
	warnings := make([]string, 0)

	cpuInfo, cpuWarn := d.detectCPU()
	warnings = append(warnings, cpuWarn...)

	memInfo, memWarn := d.detectMemory()
	warnings = append(warnings, memWarn...)

	cgroupInfo, cgroupWarn := d.detectCgroup()
	warnings = append(warnings, cgroupWarn...)

	kernelRelease, kernelWarn := d.detectKernelRelease()
	warnings = append(warnings, kernelWarn...)

	hostInfo := schema.HostInfo{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		KernelRelease: kernelRelease,
		CPU:           cpuInfo,
		Memory:        memInfo,
		Cgroup:        cgroupInfo,
	}

	procContext, procWarn := d.detectProcess()
	warnings = append(warnings, procWarn...)

	return &schema.EnvironmentSnapshot{
		Host:     hostInfo,
		Process:  procContext,
		Warnings: warnings,
	}, nil
}
