//go:build minimal

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/klauspost/compress/zstd"
)

func TestMinimalStub_MetaCommandRejection(t *testing.T) {
	idx := &format.Index{
		Version:     2,
		AppName:     "minimalapp",
		TargetOS:    "linux",
		TargetArch:  "amd64",
		CreatedUnix: time.Now().Unix(),
		Variants: []format.VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 50, UncompressedSize: 100, SHA256: "abc", Compression: "zstd"},
		},
	}
	hostInfo := microarch.Info{OS: "linux", Arch: "amd64", Level: "v3"}
	policyRes := microarch.PolicyResult{SelectedVariant: "v1"}

	metaFlags := []string{
		"--microfat:info",
		"--microfat:trim",
		"--microfat:trim-to=/tmp/out",
		"--microfat:optimize",
		"--microfat:prewarm",
		"--microfat:help",
	}

	for _, flag := range metaFlags {
		t.Run(flag, func(t *testing.T) {
			handled, err := handleMetaCommand(flag, "/tmp/bin", nil, 1000, idx, hostInfo, &idx.Variants[0], policyRes)
			if !handled {
				t.Fatalf("expected handleMetaCommand(%q) to return handled=true", flag)
			}
			if err == nil || !strings.Contains(err.Error(), "meta-commands are disabled in minimal launcher stub profile") {
				t.Fatalf("expected error indicating meta-commands disabled, got: %v", err)
			}
		})
	}

	handled, err := handleMetaCommand("normal_arg", "/tmp/bin", nil, 1000, idx, hostInfo, &idx.Variants[0], policyRes)
	if handled {
		t.Fatalf("expected handleMetaCommand to return false for non-meta command")
	}
	if err != nil {
		t.Fatalf("expected no error for non-meta command, got %v", err)
	}
}

func TestMinimalStub_TelemetryJSON(t *testing.T) {
	t.Run("error_telemetry", func(t *testing.T) {
		e := format.ErrorTelemetry{
			Event:             format.EventError,
			TimestampUnixNano: 1234567890,
			HostArch:          "amd64",
			HostLevel:         "v3",
			SelectedVariant:   "v1",
			PolicyApplied:     "force_level",
			PolicyReason:      "override",
			Stage:             "stage1",
			Error:             "something \"failed\"\n",
			Details:           "detail line",
			Hint:              "hint text",
		}
		jsonStr := formatErrorTelemetryJSON(e)
		if !strings.Contains(jsonStr, `"event":"dispatch_error"`) {
			t.Fatalf("missing event: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"host_arch":"amd64"`) {
			t.Fatalf("missing host_arch: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"error":"something \"failed\"\n"`) {
			t.Fatalf("missing error: %s", jsonStr)
		}
	})

	t.Run("dispatch_telemetry", func(t *testing.T) {
		d := format.DispatchTelemetry{
			Event:                   format.EventDispatch,
			TimestampUnixNano:       1234567890,
			HostArch:                "amd64",
			HostLevel:               "v3",
			SelectedVariant:         "v3",
			SelectedSHA256:          "abcdef",
			SelectedSizeBytes:       1024,
			ExecMode:                "memfd",
			PolicyApplied:           "safe",
			PolicyReason:            "default",
			CgroupVersion:           2,
			CgroupMemLimitBytes:     1073741824,
			CgroupCPUQuota:          2.5,
			GOMEMLIMIT:              "900MiB",
			GOMAXPROCS:              "2",
			DecompressionDurationUs: 150,
			TotalLauncherUs:         300,
		}
		jsonStr := formatDispatchTelemetryJSON(d)
		if !strings.Contains(jsonStr, `"event":"dispatch"`) {
			t.Fatalf("missing event: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"exec_mode":"memfd"`) {
			t.Fatalf("missing exec_mode: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"cgroup_version":2`) {
			t.Fatalf("missing cgroup_version: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"cgroup_cpu_quota":2.5`) {
			t.Fatalf("missing cgroup_cpu_quota: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"gomemlimit":"900MiB"`) {
			t.Fatalf("missing gomemlimit: %s", jsonStr)
		}
	})
}

func TestMinimalStub_DispatchExecution(t *testing.T) {
	tempDir := t.TempDir()
	fatBinaryPath := filepath.Join(tempDir, "fat_app")

	payloadRaw := []byte("#!/bin/sh\necho 'minimal hello'\n")
	h := sha256.Sum256(payloadRaw)
	shaHex := hex.EncodeToString(h[:])

	var compBuf bytes.Buffer
	enc, err := zstd.NewWriter(&compBuf)
	if err != nil {
		t.Fatalf("zstd new writer: %v", err)
	}
	if _, err := enc.Write(payloadRaw); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	compressedPayload := compBuf.Bytes()

	stubHeader := []byte("MICROFAT_MINIMAL_STUB_HEADER")
	offset := int64(len(stubHeader))

	idx := &format.Index{
		Version:     format.FormatVersion2,
		AppName:     "minimal_test",
		TargetOS:    "linux",
		TargetArch:  "amd64",
		CreatedUnix: time.Now().Unix(),
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           offset,
				CompressedSize:   int64(len(compressedPayload)),
				UncompressedSize: int64(len(payloadRaw)),
				SHA256:           shaHex,
				Compression:      "zstd",
			},
		},
	}

	f, err := os.OpenFile(fatBinaryPath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		t.Fatalf("open fat binary: %v", err)
	}
	if _, err := f.Write(stubHeader); err != nil {
		t.Fatalf("write stub header: %v", err)
	}
	if _, err := f.Write(compressedPayload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	currentOffset := offset + int64(len(compressedPayload))
	if _, err := format.WriteIndexAndTrailerWithVersion(f, idx, currentOffset, format.FormatVersion2); err != nil {
		t.Fatalf("write trailer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	// Test runBinary
	oldExec := execveFunc
	var interceptedPath string
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		interceptedPath = argv0
		return nil
	}
	defer func() { execveFunc = oldExec }()

	if err := runBinary(fatBinaryPath); err != nil {
		t.Fatalf("runBinary failed: %v", err)
	}
	if interceptedPath == "" {
		t.Fatalf("expected interceptedPath to not be empty")
	}
}
