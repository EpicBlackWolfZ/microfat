package env

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

var benchmarkWhitelistedEnvVars = []string{
	"GODEBUG",
	"GOAMD64",
	"GOARM64",
	"GOTOOLCHAIN",
	"MICROFAT_PROFILE",
	"MICROFAT_NO_MEMFD",
	"MICROFAT_CACHE_DIR",
	"MICROFAT_VERBOSE",
}

const (
	envGOMAXPROCS        = "GOMAXPROCS"
	envGOMEMLIMIT        = "GOMEMLIMIT"
	envMicrofatAffinity  = "MICROFAT_AFFINITY"
	procCgroupInit       = "1/cgroup"
	dockerEnvIndicator   = ".dockerenv"
	podmanEnvIndicator   = "run/.containerenv"
)

func (d *Detector) detectProcess() (schema.ProcessContext, []string) {
	warnings := make([]string, 0)

	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
		warnings = append(warnings, "failed to resolve executable path: "+err.Error())
	}

	var configuredMaxProcs *int
	if val, ok := os.LookupEnv(envGOMAXPROCS); ok {
		if parsed, pErr := strconv.Atoi(strings.TrimSpace(val)); pErr == nil && parsed > 0 {
			configuredMaxProcs = &parsed
		}
	}
	effectiveMaxProcs := runtime.GOMAXPROCS(0)

	var configuredMemLimit *string
	if val, ok := os.LookupEnv(envGOMEMLIMIT); ok {
		trimmed := strings.TrimSpace(val)
		configuredMemLimit = &trimmed
	}

	// Read effective memory limit using runtime/debug without permanently modifying it
	curMemLimit := debug.SetMemoryLimit(-1)
	debug.SetMemoryLimit(curMemLimit)

	var effectiveMemLimit *int64
	if curMemLimit > 0 && curMemLimit != math.MaxInt64 {
		v := curMemLimit
		effectiveMemLimit = &v
	}

	envVars := make([]schema.EnvVar, 0, len(benchmarkWhitelistedEnvVars))
	for _, name := range benchmarkWhitelistedEnvVars {
		val, isSet := os.LookupEnv(name)
		var valPtr *string
		if isSet {
			v := val
			valPtr = &v
		}
		envVars = append(envVars, schema.EnvVar{
			Name:  name,
			Value: valPtr,
			IsSet: isSet,
		})
	}

	inContainer := d.isInContainer()

	requestedAffinity := parseRequestedAffinity()
	effectiveAffinity, affWarn := d.getEffectiveCPUAffinity()
	if len(affWarn) > 0 {
		warnings = append(warnings, affWarn...)
	}

	return schema.ProcessContext{
		PID:                  os.Getpid(),
		ExecutablePath:       execPath,
		Args:                 os.Args,
		GoVersion:            runtime.Version(),
		GOMAXPROCSConfigured: configuredMaxProcs,
		GOMAXPROCSEffective:  effectiveMaxProcs,
		GOMEMLIMITConfigured: configuredMemLimit,
		GOMEMLIMITEffective:  effectiveMemLimit,
		EnvironmentVariables: envVars,
		InContainer:          inContainer,
		RequestedAffinity:    requestedAffinity,
		EffectiveAffinity:    effectiveAffinity,
	}, warnings
}

func parseRequestedAffinity() []int {
	raw, ok := os.LookupEnv(envMicrofatAffinity)
	if !ok {
		return nil
	}
	parts := strings.Split(raw, ",")
	res := make([]int, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if coreID, err := strconv.Atoi(trimmed); err == nil && coreID >= 0 {
			res = append(res, coreID)
		}
	}
	return res
}

func (d *Detector) isInContainer() bool {
	// 1. Check container flag files
	if _, err := os.Stat("/" + dockerEnvIndicator); err == nil {
		return true
	}
	if _, err := os.Stat("/" + podmanEnvIndicator); err == nil {
		return true
	}

	// 2. Check init cgroup file
	proc1Cgroup := filepath.Join(d.procPath, procCgroupInit)
	if fileContainsContainerTokens(proc1Cgroup) {
		return true
	}

	// 3. Check process cgroup file
	if fileContainsContainerTokens(d.procCgroupPath) {
		return true
	}

	return false
}

func fileContainsContainerTokens(path string) bool {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(scanner.Text())
		if strings.Contains(line, "docker") ||
			strings.Contains(line, "kubepods") ||
			strings.Contains(line, "containerd") ||
			strings.Contains(line, "lxc") ||
			strings.Contains(line, "libpod") ||
			strings.Contains(line, "podman") ||
			strings.Contains(line, "crio") {
			return true
		}
	}
	return false
}
