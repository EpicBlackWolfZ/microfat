// Package main implements zero-reflection JSON formatting for launcher telemetry.
package main

import (
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// formatErrorTelemetryJSON formats ErrorTelemetry as JSON without using reflection or encoding/json.
func formatErrorTelemetryJSON(e format.ErrorTelemetry) string {
	var sb strings.Builder
	sb.WriteString(`{"event":"`)
	sb.WriteString(format.EscapeJSONString(e.Event))
	sb.WriteString(`","timestamp_unix_nano":`)
	sb.WriteString(strconv.FormatInt(e.TimestampUnixNano, 10))

	if e.HostArch != "" {
		sb.WriteString(`,"host_arch":"`)
		sb.WriteString(format.EscapeJSONString(e.HostArch))
		sb.WriteString(`"`)
	}
	if e.HostLevel != "" {
		sb.WriteString(`,"host_level":"`)
		sb.WriteString(format.EscapeJSONString(e.HostLevel))
		sb.WriteString(`"`)
	}
	if e.SelectedVariant != "" {
		sb.WriteString(`,"selected_variant":"`)
		sb.WriteString(format.EscapeJSONString(e.SelectedVariant))
		sb.WriteString(`"`)
	}
	if e.PolicyApplied != "" {
		sb.WriteString(`,"policy_applied":"`)
		sb.WriteString(format.EscapeJSONString(e.PolicyApplied))
		sb.WriteString(`"`)
	}
	if e.PolicyReason != "" {
		sb.WriteString(`,"policy_reason":"`)
		sb.WriteString(format.EscapeJSONString(e.PolicyReason))
		sb.WriteString(`"`)
	}
	sb.WriteString(`,"stage":"`)
	sb.WriteString(format.EscapeJSONString(e.Stage))
	sb.WriteString(`","error":"`)
	sb.WriteString(format.EscapeJSONString(e.Error))
	sb.WriteString(`"`)

	if e.Details != "" {
		sb.WriteString(`,"details":"`)
		sb.WriteString(format.EscapeJSONString(e.Details))
		sb.WriteString(`"`)
	}
	if e.Hint != "" {
		sb.WriteString(`,"hint":"`)
		sb.WriteString(format.EscapeJSONString(e.Hint))
		sb.WriteString(`"`)
	}
	sb.WriteString(`}`)
	return sb.String()
}

// formatDispatchTelemetryJSON formats DispatchTelemetry as JSON without using reflection or encoding/json.
func formatDispatchTelemetryJSON(d format.DispatchTelemetry) string {
	var sb strings.Builder
	sb.WriteString(`{"event":"`)
	sb.WriteString(format.EscapeJSONString(d.Event))
	sb.WriteString(`","timestamp_unix_nano":`)
	sb.WriteString(strconv.FormatInt(d.TimestampUnixNano, 10))

	if d.HostArch != "" {
		sb.WriteString(`,"host_arch":"`)
		sb.WriteString(format.EscapeJSONString(d.HostArch))
		sb.WriteString(`"`)
	}
	if d.HostLevel != "" {
		sb.WriteString(`,"host_level":"`)
		sb.WriteString(format.EscapeJSONString(d.HostLevel))
		sb.WriteString(`"`)
	}
	if d.SelectedVariant != "" {
		sb.WriteString(`,"selected_variant":"`)
		sb.WriteString(format.EscapeJSONString(d.SelectedVariant))
		sb.WriteString(`"`)
	}
	if d.SelectedSHA256 != "" {
		sb.WriteString(`,"selected_sha256":"`)
		sb.WriteString(format.EscapeJSONString(d.SelectedSHA256))
		sb.WriteString(`"`)
	}
	if d.SelectedSizeBytes > 0 {
		sb.WriteString(`,"selected_size_bytes":`)
		sb.WriteString(strconv.FormatInt(d.SelectedSizeBytes, 10))
	}
	sb.WriteString(`,"exec_mode":"`)
	sb.WriteString(format.EscapeJSONString(d.ExecMode))
	sb.WriteString(`"`)

	if d.PolicyApplied != "" {
		sb.WriteString(`,"policy_applied":"`)
		sb.WriteString(format.EscapeJSONString(d.PolicyApplied))
		sb.WriteString(`"`)
	}
	if d.PolicyReason != "" {
		sb.WriteString(`,"policy_reason":"`)
		sb.WriteString(format.EscapeJSONString(d.PolicyReason))
		sb.WriteString(`"`)
	}
	if d.CgroupVersion > 0 {
		sb.WriteString(`,"cgroup_version":`)
		sb.WriteString(strconv.Itoa(d.CgroupVersion))
	}
	if d.CgroupMemLimitBytes > 0 {
		sb.WriteString(`,"cgroup_mem_limit_bytes":`)
		sb.WriteString(strconv.FormatInt(d.CgroupMemLimitBytes, 10))
	}
	if d.CgroupCPUQuota > 0 {
		sb.WriteString(`,"cgroup_cpu_quota":`)
		sb.WriteString(strconv.FormatFloat(d.CgroupCPUQuota, 'f', -1, 64))
	}
	if d.GOMEMLIMIT != "" {
		sb.WriteString(`,"gomemlimit":"`)
		sb.WriteString(format.EscapeJSONString(d.GOMEMLIMIT))
		sb.WriteString(`"`)
	}
	if d.GOMAXPROCS != "" {
		sb.WriteString(`,"gomaxprocs":"`)
		sb.WriteString(format.EscapeJSONString(d.GOMAXPROCS))
		sb.WriteString(`"`)
	}
	if d.GOGC != "" {
		sb.WriteString(`,"gogc":"`)
		sb.WriteString(format.EscapeJSONString(d.GOGC))
		sb.WriteString(`"`)
	}
	if d.GCProfile != "" {
		sb.WriteString(`,"gc_profile":"`)
		sb.WriteString(format.EscapeJSONString(d.GCProfile))
		sb.WriteString(`"`)
	}
	if d.DecompressionDurationUs > 0 {
		sb.WriteString(`,"decompression_duration_us":`)
		sb.WriteString(strconv.FormatInt(d.DecompressionDurationUs, 10))
	}
	sb.WriteString(`,"total_launcher_us":`)
	sb.WriteString(strconv.FormatInt(d.TotalLauncherUs, 10))
	sb.WriteString(`}`)
	return sb.String()
}
