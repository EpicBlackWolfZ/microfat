package schema

// EnvironmentSnapshot models the complete captured hardware and process execution context.
type EnvironmentSnapshot struct {
	Host     HostInfo       `json:"host"`
	Process  ProcessContext `json:"process"`
	Warnings []string       `json:"warnings"`
}

// HostInfo describes host operating system, architecture, kernel, CPU, memory, and cgroup limits.
type HostInfo struct {
	OS            string     `json:"os"`
	Arch          string     `json:"arch"`
	KernelRelease string     `json:"kernel_release"`
	CPU           CPUInfo    `json:"cpu"`
	Memory        MemoryInfo `json:"memory"`
	Cgroup        CgroupInfo `json:"cgroup"`
}

// CPUInfo describes host CPU topology, features, scaling governor, and operational frequencies.
type CPUInfo struct {
	ModelName       string   `json:"model_name"`
	MicroarchLevel  string   `json:"microarch_level"`
	Cores           *int     `json:"cores"`
	Sockets         *int     `json:"sockets"`
	NUMANodes       *int     `json:"numa_nodes"`
	Flags           []string `json:"flags"`
	ScalingGovernor *string  `json:"scaling_governor"`
	MinFreqKHz      *uint64  `json:"min_freq_khz"`
	MaxFreqKHz      *uint64  `json:"max_freq_khz"`
}

// MemoryInfo captures total and available host RAM.
type MemoryInfo struct {
	TotalBytes     *uint64 `json:"total_bytes"`
	AvailableBytes *uint64 `json:"available_bytes"`
}

// CgroupInfo models detected cgroup resource ceilings and periods.
type CgroupInfo struct {
	Version        string               `json:"version"`
	MemoryMaxBytes ResourceLimit[int64] `json:"memory_max_bytes"`
	CPUQuotaUs     ResourceLimit[int64] `json:"cpu_quota_us"`
	CPUPeriodUs    *int64               `json:"cpu_period_us"`
}

// ProcessContext records process provenance, configuration vs effective runtime limits, and affinity.
type ProcessContext struct {
	PID                  int      `json:"pid"`
	ExecutablePath       string   `json:"executable_path"`
	Args                 []string `json:"args"`
	GoVersion            string   `json:"go_version"`
	GOMAXPROCSConfigured *int     `json:"gomaxprocs_configured"`
	GOMAXPROCSEffective  int      `json:"gomaxprocs_effective"`
	GOMEMLIMITConfigured *string  `json:"gomemlimit_configured"`
	GOMEMLIMITEffective  *int64   `json:"gomemlimit_effective"`
	EnvironmentVariables []EnvVar `json:"environment_variables"`
	InContainer          bool     `json:"in_container"`
	RequestedAffinity    []int    `json:"requested_affinity"`
	EffectiveAffinity    []int    `json:"effective_affinity"`
}

// EnvVar captures the name, nullable value, and presence state of an environment variable.
type EnvVar struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
	IsSet bool    `json:"is_set"`
}
