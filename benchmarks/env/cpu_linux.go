//go:build linux

package env

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	maxCPUAffinityBits = 1024
	kibibyteInBytes    = 1024

	procCPUInfoFile        = "cpuinfo"
	procMemInfoFile        = "meminfo"
	procOSReleaseFile      = "sys/kernel/osrelease"
	sysNodeDir             = "devices/system/node"
	sysCPU0FreqDir         = "devices/system/cpu/cpu0/cpufreq"
	scalingGovernorFile    = "scaling_governor"
	scalingMinFreqFile     = "scaling_min_freq"
	scalingMaxFreqFile     = "scaling_max_freq"
	cpuinfoMinFreqFile     = "cpuinfo_min_freq"
	cpuinfoMaxFreqFile     = "cpuinfo_max_freq"
	memInfoFieldMemTotal   = "MemTotal:"
	memInfoFieldMemAvail   = "MemAvailable:"

	cpuInfoKeyValueParts  = 2
	minMemInfoFieldCount  = 2
)

func (d *Detector) detectKernelRelease() (string, []string) {
	relPath := filepath.Join(d.procPath, procOSReleaseFile)
	if line, err := readTrimmedLine(relPath); err == nil && line != "" {
		return line, nil
	}

	var buf unix.Utsname
	if err := unix.Uname(&buf); err == nil {
		release := unix.ByteSliceToString(buf.Release[:])
		if release != "" {
			return release, nil
		}
	}

	return "", []string{"kernel release unavailable"}
}

func (d *Detector) detectCPU() (schema.CPUInfo, []string) {
	var warnings []string
	cpuInfoPath := filepath.Join(d.procPath, procCPUInfoFile)

	modelName, flags, cores, sockets := parseLinuxCPUInfo(cpuInfoPath)
	if modelName == "" {
		modelName = microarch.Detect().Arch
		warnings = append(warnings, "CPU model name unavailable from cpuinfo")
	}

	if len(flags) == 0 {
		flags = microarch.Detect().Features
	}

	microarchLevel := microarch.CurrentLevel()

	numaNodes := d.detectNUMANodes()
	if numaNodes == nil {
		warnings = append(warnings, "NUMA node topology unavailable")
	}

	governor, minFreq, maxFreq, freqWarn := d.detectCPUFreq()
	if len(freqWarn) > 0 {
		warnings = append(warnings, freqWarn...)
	}

	return schema.CPUInfo{
		ModelName:       modelName,
		MicroarchLevel:  microarchLevel,
		Cores:           cores,
		Sockets:         sockets,
		NUMANodes:       numaNodes,
		Flags:           flags,
		ScalingGovernor: governor,
		MinFreqKHz:      minFreq,
		MaxFreqKHz:      maxFreq,
	}, warnings
}

type cpuInfoState struct {
	modelName      string
	flags          []string
	physicalIDs    map[string]struct{}
	uniqueCores    map[string]struct{}
	coreCount      int
	processorCount int
	curPhys        string
	curCore        string
}

func newCPUInfoState() *cpuInfoState {
	return &cpuInfoState{
		physicalIDs: make(map[string]struct{}),
		uniqueCores: make(map[string]struct{}),
	}
}

func (s *cpuInfoState) recordCore() {
	if s.curCore != "" {
		phys := s.curPhys
		if phys == "" {
			phys = "0"
		}
		s.uniqueCores[phys+":"+s.curCore] = struct{}{}
	}
	s.curPhys = ""
	s.curCore = ""
}

func (s *cpuInfoState) processLine(line string) {
	if !strings.Contains(line, ":") {
		if strings.TrimSpace(line) == "" {
			s.recordCore()
		}
		return
	}
	parts := strings.SplitN(line, ":", cpuInfoKeyValueParts)
	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])

	switch key {
	case "model name", "Hardware", "Processor":
		if s.modelName == "" && val != "" {
			s.modelName = val
		}
	case "flags", "Features":
		if len(s.flags) == 0 && val != "" {
			s.flags = strings.Fields(val)
		}
	case "physical id":
		if val != "" {
			s.physicalIDs[val] = struct{}{}
			s.curPhys = val
		}
	case "core id":
		if val != "" {
			s.curCore = val
		}
	case "cpu cores":
		if c, parseErr := strconv.Atoi(val); parseErr == nil && c > s.coreCount {
			s.coreCount = c
		}
	case "processor":
		s.recordCore()
		s.processorCount++
	}
}

func (s *cpuInfoState) resolveCores() *int {
	switch {
	case len(s.uniqueCores) > 0:
		c := len(s.uniqueCores)
		return &c
	case s.coreCount > 0:
		c := s.coreCount
		return &c
	case s.processorCount > 0:
		c := s.processorCount
		return &c
	default:
		return nil
	}
}

func (s *cpuInfoState) resolveSockets() *int {
	if len(s.physicalIDs) > 0 {
		sockets := len(s.physicalIDs)
		return &sockets
	}
	return nil
}

func parseLinuxCPUInfo(path string) (modelName string, flags []string, cores *int, sockets *int) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", nil, nil, nil
	}
	defer func() { _ = f.Close() }()

	state := newCPUInfoState()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		state.processLine(scanner.Text())
	}
	state.recordCore()

	return state.modelName, state.flags, state.resolveCores(), state.resolveSockets()
}

func (d *Detector) detectNUMANodes() *int {
	nodeDirPath := filepath.Join(d.sysPath, sysNodeDir)
	entries, err := os.ReadDir(nodeDirPath)
	if err != nil {
		return nil
	}

	nodeCount := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "node") {
			suffix := strings.TrimPrefix(entry.Name(), "node")
			if _, parseErr := strconv.Atoi(suffix); parseErr == nil {
				nodeCount++
			}
		}
	}

	if nodeCount > 0 {
		return &nodeCount
	}
	return nil
}

func (d *Detector) detectCPUFreq() (*string, *uint64, *uint64, []string) {
	var warnings []string
	freqDirPath := filepath.Join(d.sysPath, sysCPU0FreqDir)
	if _, err := os.Stat(freqDirPath); err != nil {
		policy0Path := filepath.Join(d.sysPath, "devices/system/cpu/cpufreq/policy0")
		if _, pErr := os.Stat(policy0Path); pErr == nil {
			freqDirPath = policy0Path
		}
	}

	var governor *string
	govPath := filepath.Join(freqDirPath, scalingGovernorFile)
	if line, err := readTrimmedLine(govPath); err == nil && line != "" {
		governor = &line
	} else {
		warnings = append(warnings, "CPU scaling governor unavailable")
	}

	var minFreq *uint64
	minPath := filepath.Join(freqDirPath, scalingMinFreqFile)
	if line, err := readTrimmedLine(minPath); err == nil && line != "" {
		if v, pErr := strconv.ParseUint(line, 10, 64); pErr == nil {
			minFreq = &v
		}
	} else {
		fallbackMin := filepath.Join(freqDirPath, cpuinfoMinFreqFile)
		if fbLine, fbErr := readTrimmedLine(fallbackMin); fbErr == nil && fbLine != "" {
			if v, pErr := strconv.ParseUint(fbLine, 10, 64); pErr == nil {
				minFreq = &v
			}
		}
	}

	var maxFreq *uint64
	maxPath := filepath.Join(freqDirPath, scalingMaxFreqFile)
	if line, err := readTrimmedLine(maxPath); err == nil && line != "" {
		if v, pErr := strconv.ParseUint(line, 10, 64); pErr == nil {
			maxFreq = &v
		}
	} else {
		fallbackMax := filepath.Join(freqDirPath, cpuinfoMaxFreqFile)
		if fbLine, fbErr := readTrimmedLine(fallbackMax); fbErr == nil && fbLine != "" {
			if v, pErr := strconv.ParseUint(fbLine, 10, 64); pErr == nil {
				maxFreq = &v
			}
		}
	}

	if minFreq == nil && maxFreq == nil {
		warnings = append(warnings, "CPU frequency telemetry unavailable")
	}

	return governor, minFreq, maxFreq, warnings
}

func (d *Detector) detectMemory() (schema.MemoryInfo, []string) {
	memPath := filepath.Join(d.procPath, procMemInfoFile)
	f, err := os.Open(filepath.Clean(memPath))
	if err != nil {
		return schema.MemoryInfo{}, []string{"host memory telemetry unavailable: " + err.Error()}
	}
	defer func() { _ = f.Close() }()

	var totalBytes *uint64
	var availableBytes *uint64

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= minMemInfoFieldCount {
			switch fields[0] {
			case memInfoFieldMemTotal:
				if kb, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					b := kb * kibibyteInBytes
					totalBytes = &b
				}
			case memInfoFieldMemAvail:
				if kb, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					b := kb * kibibyteInBytes
					availableBytes = &b
				}
			}
		}
	}

	var warnings []string
	if totalBytes == nil {
		warnings = append(warnings, "host total memory unavailable")
	}
	if availableBytes == nil {
		warnings = append(warnings, "host available memory unavailable")
	}

	return schema.MemoryInfo{
		TotalBytes:     totalBytes,
		AvailableBytes: availableBytes,
	}, warnings
}

func (d *Detector) getEffectiveCPUAffinity() ([]int, []string) {
	var mask unix.CPUSet
	if err := unix.SchedGetaffinity(0, &mask); err != nil {
		return nil, []string{"CPU affinity probe unavailable: " + err.Error()}
	}

	count := mask.Count()
	res := make([]int, 0, count)
	for i := 0; i < maxCPUAffinityBits; i++ {
		if mask.IsSet(i) {
			res = append(res, i)
			if len(res) == count {
				break
			}
		}
	}

	return res, nil
}
