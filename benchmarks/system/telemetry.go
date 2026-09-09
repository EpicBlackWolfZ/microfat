package system

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const kibibyte = 1024

// ReadProcess records kernel counters separately from sampled current memory values.
func ReadProcess(procRoot string, pid int, phase string) map[string]schema.Measurement {
	result := make(map[string]schema.Measurement)
	// #nosec G304 -- explicit proc root, numeric PID, fixed status filename.
	status, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
	if err != nil {
		result["rss_bytes"] = schema.Unavailable("bytes", phase, "proc/status", err.Error())
		return result
	}
	fields := ParseFields(string(status))
	for _, field := range []struct {
		key, name, unit string
		scale           float64
	}{
		{"VmRSS", "rss_bytes", "bytes", kibibyte}, {"VmSize", "vss_bytes", "bytes", kibibyte},
		{"VmHWM", "lifetime_peak_rss_bytes", "bytes", kibibyte},
		{"voluntary_ctxt_switches", "voluntary_context_switches", "count", 1},
		{"nonvoluntary_ctxt_switches", "involuntary_context_switches", "count", 1},
	} {
		value, err := firstNumber(fields[field.key])
		if err != nil {
			result[field.name] = schema.Unavailable(field.unit, phase, "proc/status", err.Error())
		} else {
			result[field.name] = schema.Measured(value*field.scale, field.unit, phase, "proc/status")
		}
	}
	return result
}

func ParseFields(data string) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			key, value, ok = strings.Cut(line, " ")
		}
		if ok {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}

func firstNumber(value string) (float64, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, errors.New("counter unavailable")
	}
	number, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || !schema.Finite(number) || number < 0 {
		return 0, errors.New("invalid counter")
	}
	return number, nil
}

func (s *Sandbox) Read(phase string) map[string]schema.Measurement {
	result := make(map[string]schema.Measurement)
	for _, root := range s.Paths {
		for _, file := range []string{"memory.current", "memory.peak", "memory.usage_in_bytes", "memory.max_usage_in_bytes"} {
			// #nosec G304 -- benchmark-owned cgroup root and fixed controller filename.
			data, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				continue
			}
			value, err := firstNumber(string(data))
			if err != nil {
				result[file] = schema.Unavailable("bytes", phase, "cgroup/"+file, err.Error())
				continue
			}
			result[file] = schema.Measured(value, "bytes", phase, "cgroup/"+file)
		}
		for _, file := range []string{"cpu.stat", "memory.stat", "memory.events"} {
			// #nosec G304 -- benchmark-owned cgroup root and fixed controller filename.
			data, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				continue
			}
			for key, text := range ParseFields(string(data)) {
				value, err := firstNumber(text)
				if err != nil {
					continue
				}
				unit := "count"
				if file == "memory.stat" && isMemoryBytes(key) {
					unit = "bytes"
				}
				if strings.HasSuffix(key, "_usec") {
					unit = "us"
				}
				if key == "throttled_time" {
					unit = "ns"
				}
				result[file+"/"+key] = schema.Measured(value, unit, phase, "cgroup/"+file)
			}
		}
	}
	if len(result) == 0 {
		result["cgroup_memory_bytes"] = schema.Unavailable("bytes", phase, "cgroup", "target cgroup telemetry unavailable")
	}
	return result
}

func isMemoryBytes(key string) bool {
	for _, name := range []string{"anon", "file", "shmem", "kernel", "kernel_stack", "pagetables", "sock", "slab",
		"rss", "cache", "mapped_file", "swap", "total_rss", "total_cache", "active_anon", "inactive_anon", "active_file", "inactive_file"} {
		if key == name {
			return true
		}
	}
	return false
}

func Delta(before, after schema.Measurement) schema.Measurement {
	if before.Value == nil || after.Value == nil || before.Unit != after.Unit || *after.Value < *before.Value {
		return schema.Unavailable(after.Unit, after.Phase, after.Source, "counter unavailable or reset")
	}
	result := schema.Measured(*after.Value-*before.Value, after.Unit, after.Phase, after.Source)
	result.Kind = "derived"
	return result
}

func ReadHost(sysRoot string) map[string]string {
	result := make(map[string]string)
	for _, name := range []string{"scaling_governor", "scaling_cur_freq", "scaling_min_freq", "scaling_max_freq"} {
		matches, err := filepath.Glob(filepath.Join(sysRoot, "devices/system/cpu/cpu*/cpufreq", name))
		if err != nil {
			continue
		}
		for _, file := range matches {
			if data, err := os.ReadFile(file); err == nil { // #nosec G304 -- glob of fixed sysfs CPU governor/frequency files.
				result[file] = strings.TrimSpace(string(data))
			}
		}
	}
	if len(result) == 0 {
		result["frequency"] = controlUnavailable
	}
	for _, pattern := range []string{"devices/system/cpu/cpu*/topology/thread_siblings_list",
		"devices/system/cpu/cpu*/thermal_throttle/*_throttle_count", "class/thermal/thermal_zone*/temp"} {
		matches, _ := filepath.Glob(filepath.Join(sysRoot, pattern))
		for _, file := range matches {
			if data, err := os.ReadFile(file); err == nil { // #nosec G304 -- fixed sysfs topology and thermal glob.
				result[file] = strings.TrimSpace(string(data))
			}
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		result["loadavg"] = strings.TrimSpace(string(data))
	}
	return result
}

func Uncontrolled(controls []schema.Control) []string {
	var warnings []string
	for _, c := range controls {
		if c.State != "applied" {
			warnings = append(warnings, fmt.Sprintf("uncontrolled %s: %s", c.Name, c.Reason))
		}
	}
	return warnings
}
