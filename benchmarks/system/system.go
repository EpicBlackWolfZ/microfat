// Package system separates requested resource controls from observed target telemetry.
package system

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	controlUnavailable = "unavailable"
	DefaultPeriod      = int64(100000)
	MaxCPU             = 1024
	defaultDirMode     = 0o700
	controlMode        = 0o600
)

type Options struct {
	Affinity    []int  `json:"affinity"`
	CgroupRoot  string `json:"cgroup_root"`
	MemoryRoot  string `json:"memory_root"`
	Version     string `json:"version"`
	CPUQuotaUS  int64  `json:"cpu_quota_us"`
	CPUPeriodUS int64  `json:"cpu_period_us"`
	MemoryBytes int64  `json:"memory_bytes"`
}

func (o Options) Validate() error {
	if o.CPUQuotaUS < 0 || o.MemoryBytes < 0 || o.CPUPeriodUS <= 0 {
		return errors.New("resource limits must be nonnegative and CPU period positive")
	}
	if o.Version != "" && o.Version != "v1" && o.Version != "v2" {
		return errors.New("unsupported cgroup version")
	}
	seen := make(map[int]bool)
	for _, cpu := range o.Affinity {
		if cpu < 0 || cpu >= MaxCPU || seen[cpu] {
			return errors.New("invalid or duplicate CPU affinity")
		}
		seen[cpu] = true
	}
	for _, root := range []string{o.CgroupRoot, o.MemoryRoot} {
		if root != "" && !filepath.IsAbs(root) {
			return errors.New("cgroup root must be absolute")
		}
	}
	return nil
}

type Sandbox struct {
	Options    Options
	Controls   []schema.Control
	Paths      []string
	Procs      []string
	mkdirGroup func(string) (string, error)
}

func Prepare(options Options) (*Sandbox, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	sandbox := &Sandbox{Options: options}
	if len(options.Affinity) == 0 {
		sandbox.Controls = append(sandbox.Controls, schema.Control{Name: "affinity", State: controlUnavailable, Reason: "not requested"})
	} else {
		effective, err := SupportedAffinity(options.Affinity)
		control := schema.Control{Name: "affinity", Requested: CPUList(options.Affinity), Effective: CPUList(effective), State: "applied"}
		if err != nil {
			control.State, control.Reason = controlUnavailable, err.Error()
			sandbox.Options.Affinity = nil
		}
		sandbox.Controls = append(sandbox.Controls, control)
	}
	if options.CgroupRoot == "" {
		sandbox.Controls = append(sandbox.Controls, schema.Control{Name: "cgroup", State: controlUnavailable,
			Reason: "no delegated cgroup root configured; inherited limits are observed only"})
		return sandbox, nil
	}
	setupErr := CheckCgroupRoot(options.CgroupRoot, options.Version)
	if setupErr == nil && options.Version == "v1" {
		setupErr = CheckCgroupRoot(options.MemoryRoot, options.Version)
	}
	if setupErr == nil {
		setupErr = sandbox.prepareCgroups()
	}
	if err := setupErr; err != nil {
		cleanupErr := sandbox.Close()
		for i := range sandbox.Controls {
			if sandbox.Controls[i].Name != "affinity" {
				sandbox.Controls[i].State = controlUnavailable
				sandbox.Controls[i].Reason = "resource setup rolled back"
			}
		}
		sandbox.Controls = append(sandbox.Controls, schema.Control{Name: "cgroup", State: controlUnavailable,
			Requested: options.Version, Reason: errors.Join(err, cleanupErr).Error()})
	}
	return sandbox, nil
}

func (s *Sandbox) prepareCgroups() error {
	o := s.Options
	if o.Version == "" || o.Version == "v2" {
		cpu := "max " + strconv.FormatInt(o.CPUPeriodUS, 10)
		if o.CPUQuotaUS > 0 {
			cpu = fmt.Sprintf("%d %d", o.CPUQuotaUS, o.CPUPeriodUS)
		}
		memory := "max"
		if o.MemoryBytes > 0 {
			memory = strconv.FormatInt(o.MemoryBytes, 10)
		}
		return s.createGroup(o.CgroupRoot, map[string]string{"cpu.max": cpu, "memory.max": memory})
	}
	if o.MemoryRoot == "" {
		return errors.New("v1 requires a separate memory controller root")
	}
	quota := int64(-1)
	if o.CPUQuotaUS > 0 {
		quota = o.CPUQuotaUS
	}
	if err := s.createGroup(o.CgroupRoot, map[string]string{"cpu.cfs_quota_us": strconv.FormatInt(quota, 10),
		"cpu.cfs_period_us": strconv.FormatInt(o.CPUPeriodUS, 10)}); err != nil {
		return err
	}
	limits := map[string]string{}
	if o.MemoryBytes > 0 {
		limits["memory.limit_in_bytes"] = strconv.FormatInt(o.MemoryBytes, 10)
	}
	return s.createGroup(o.MemoryRoot, limits)
}

func (s *Sandbox) createGroup(root string, values map[string]string) error {
	// MkdirTemp creates only this run's subgroup; never changes a parent controller configuration.
	mkdir := s.mkdirGroup
	if mkdir == nil {
		mkdir = func(root string) (string, error) { return os.MkdirTemp(root, "microfat-bench-") }
	}
	dir, err := mkdir(root)
	if err != nil {
		return err
	}
	s.Paths = append(s.Paths, dir)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		file := filepath.Join(dir, key)
		// Require kernel-created controller files. Do not fake controls on an ordinary filesystem.
		if _, err := os.Stat(file); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(values[key]), controlMode); err != nil {
			return err
		}
		effective, err := os.ReadFile(file) // #nosec G304 -- fixed controller name in this benchmark-owned subgroup.
		if err != nil {
			return err
		}
		if !effectiveLimit(key, values[key], strings.TrimSpace(string(effective)), int64(os.Getpagesize())) {
			return fmt.Errorf("control %s did not take effect", key)
		}
		s.Controls = append(s.Controls, schema.Control{Name: key, Requested: values[key],
			Effective: strings.TrimSpace(string(effective)), State: "applied"})
	}
	s.Procs = append(s.Procs, filepath.Join(dir, "cgroup.procs"))
	return nil
}

func effectiveLimit(name, requested, effective string, page int64) bool {
	if requested == effective {
		return true
	}
	if name != "memory.limit_in_bytes" && name != "memory.max" {
		return false
	}
	want, wantErr := strconv.ParseInt(requested, 10, 64)
	got, gotErr := strconv.ParseInt(effective, 10, 64)
	return wantErr == nil && gotErr == nil && want > 0 && page > 0 && got == want/page*page
}

func (s *Sandbox) Close() error {
	var result error
	for i := len(s.Paths) - 1; i >= 0; i-- {
		err := os.Remove(s.Paths[i])
		if !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	s.Procs = nil
	if result == nil {
		s.Paths = nil
	}
	return result
}

func CPUList(cpus []int) string {
	values := make([]string, len(cpus))
	for i, cpu := range cpus {
		values[i] = strconv.Itoa(cpu)
	}
	return strings.Join(values, ",")
}

type ChildConfig struct {
	Path     string   `json:"path"`
	Args     []string `json:"args"`
	Env      []string `json:"env"`
	Affinity []int    `json:"affinity"`
	Procs    []string `json:"procs"`
}

func (s *Sandbox) Wrap(helper string, spec process.Spec) process.Spec {
	// ChildConfig contains only strings/integers, so encoding cannot fail.
	data, _ := json.Marshal(ChildConfig{Path: spec.Path, Args: spec.Args, Env: spec.Env,
		Affinity: s.Options.Affinity, Procs: s.Procs})
	return process.Spec{Path: helper, Args: []string{"benchmark", "child", string(data)}, Env: spec.Env, Dir: spec.Dir}
}

func ParseChild(data []byte) (ChildConfig, error) {
	var cfg ChildConfig
	if len(data) > process.MaxOutput {
		return cfg, errors.New("bootstrap configuration too large")
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if !filepath.IsAbs(cfg.Path) {
		return cfg, errors.New("child executable must be absolute")
	}
	for _, group := range cfg.Procs {
		if !filepath.IsAbs(group) || filepath.Base(group) != "cgroup.procs" {
			return cfg, errors.New("invalid cgroup attachment")
		}
	}
	if err := (Options{Affinity: cfg.Affinity, CPUPeriodUS: DefaultPeriod}).Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
