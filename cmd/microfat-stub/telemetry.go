// Package main implements zero-reflection JSON formatting for launcher telemetry.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// formatErrorTelemetryJSON formats ErrorTelemetry as JSON without using reflection or encoding/json.
func formatErrorTelemetryJSON(e format.ErrorTelemetry) string {
	var sb strings.Builder
	sb.WriteString(`{"event":"`)
	sb.WriteString(escapeJSONString(e.Event))
	sb.WriteString(`","timestamp_unix_nano":`)
	sb.WriteString(strconv.FormatInt(e.TimestampUnixNano, 10))

	if e.HostArch != "" {
		sb.WriteString(`,"host_arch":"`)
		sb.WriteString(escapeJSONString(e.HostArch))
		sb.WriteString(`"`)
	}
	if e.HostLevel != "" {
		sb.WriteString(`,"host_level":"`)
		sb.WriteString(escapeJSONString(e.HostLevel))
		sb.WriteString(`"`)
	}
	if e.SelectedVariant != "" {
		sb.WriteString(`,"selected_variant":"`)
		sb.WriteString(escapeJSONString(e.SelectedVariant))
		sb.WriteString(`"`)
	}
	if e.PolicyApplied != "" {
		sb.WriteString(`,"policy_applied":"`)
		sb.WriteString(escapeJSONString(e.PolicyApplied))
		sb.WriteString(`"`)
	}
	if e.PolicyReason != "" {
		sb.WriteString(`,"policy_reason":"`)
		sb.WriteString(escapeJSONString(e.PolicyReason))
		sb.WriteString(`"`)
	}
	sb.WriteString(`,"stage":"`)
	sb.WriteString(escapeJSONString(e.Stage))
	sb.WriteString(`","error":"`)
	sb.WriteString(escapeJSONString(e.Error))
	sb.WriteString(`"`)

	if e.Details != "" {
		sb.WriteString(`,"details":"`)
		sb.WriteString(escapeJSONString(e.Details))
		sb.WriteString(`"`)
	}
	if e.Hint != "" {
		sb.WriteString(`,"hint":"`)
		sb.WriteString(escapeJSONString(e.Hint))
		sb.WriteString(`"`)
	}
	sb.WriteString(`}`)
	return sb.String()
}

// formatDispatchTelemetryJSON formats DispatchTelemetry as JSON without using reflection or encoding/json.
func formatDispatchTelemetryJSON(d format.DispatchTelemetry) string {
	var sb strings.Builder
	sb.WriteString(`{"event":"`)
	sb.WriteString(escapeJSONString(d.Event))
	sb.WriteString(`","timestamp_unix_nano":`)
	sb.WriteString(strconv.FormatInt(d.TimestampUnixNano, 10))

	if d.HostArch != "" {
		sb.WriteString(`,"host_arch":"`)
		sb.WriteString(escapeJSONString(d.HostArch))
		sb.WriteString(`"`)
	}
	if d.HostLevel != "" {
		sb.WriteString(`,"host_level":"`)
		sb.WriteString(escapeJSONString(d.HostLevel))
		sb.WriteString(`"`)
	}
	if d.SelectedVariant != "" {
		sb.WriteString(`,"selected_variant":"`)
		sb.WriteString(escapeJSONString(d.SelectedVariant))
		sb.WriteString(`"`)
	}
	if d.SelectedSHA256 != "" {
		sb.WriteString(`,"selected_sha256":"`)
		sb.WriteString(escapeJSONString(d.SelectedSHA256))
		sb.WriteString(`"`)
	}
	if d.SelectedSizeBytes > 0 {
		sb.WriteString(`,"selected_size_bytes":`)
		sb.WriteString(strconv.FormatInt(d.SelectedSizeBytes, 10))
	}
	sb.WriteString(`,"exec_mode":"`)
	sb.WriteString(escapeJSONString(d.ExecMode))
	sb.WriteString(`"`)

	if d.PolicyApplied != "" {
		sb.WriteString(`,"policy_applied":"`)
		sb.WriteString(escapeJSONString(d.PolicyApplied))
		sb.WriteString(`"`)
	}
	if d.PolicyReason != "" {
		sb.WriteString(`,"policy_reason":"`)
		sb.WriteString(escapeJSONString(d.PolicyReason))
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
		sb.WriteString(escapeJSONString(d.GOMEMLIMIT))
		sb.WriteString(`"`)
	}
	if d.GOMAXPROCS != "" {
		sb.WriteString(`,"gomaxprocs":"`)
		sb.WriteString(escapeJSONString(d.GOMAXPROCS))
		sb.WriteString(`"`)
	}
	if d.GOGC != "" {
		sb.WriteString(`,"gogc":"`)
		sb.WriteString(escapeJSONString(d.GOGC))
		sb.WriteString(`"`)
	}
	if d.GCProfile != "" {
		sb.WriteString(`,"gc_profile":"`)
		sb.WriteString(escapeJSONString(d.GCProfile))
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

const (
	asciiControlCutoff = 0x20
)

func escapeJSONString(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if c < asciiControlCutoff {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	return sb.String()
}
