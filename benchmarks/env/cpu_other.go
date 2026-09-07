//go:build !linux

package env

import (
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

func (d *Detector) detectKernelRelease() (string, []string) {
	return "", []string{"kernel release detection unavailable on " + runtime.GOOS}
}

func (d *Detector) detectCPU() (schema.CPUInfo, []string) {
	cores := runtime.NumCPU()
	return schema.CPUInfo{
		ModelName:      runtime.GOARCH,
		MicroarchLevel: microarch.CurrentLevel(),
		Cores:          &cores,
		Flags:          microarch.Detect().Features,
	}, []string{"detailed CPU hardware telemetry unavailable on " + runtime.GOOS}
}

func (d *Detector) detectMemory() (schema.MemoryInfo, []string) {
	return schema.MemoryInfo{}, []string{"memory telemetry unavailable on " + runtime.GOOS}
}

func (d *Detector) getEffectiveCPUAffinity() ([]int, []string) {
	return nil, []string{"CPU affinity detection unavailable on " + runtime.GOOS}
}
